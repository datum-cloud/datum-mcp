package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/datum-cloud/datum-mcp/internal/discovery"
	"gopkg.in/yaml.v3"
)

// Service implements the MCP tools using a Discovery cache.
type Service struct {
	Disc *discovery.Cache
	// Optional allow-lists for metadata keys to preserve.
	AllowedMetaAnnotations map[string]struct{}
	AllowedMetaLabels      map[string]struct{}
}

func NewService(d *discovery.Cache) *Service {
	return &Service{
		Disc: d,
		AllowedMetaAnnotations: map[string]struct{}{},
		AllowedMetaLabels:      map[string]struct{}{},
	}
}

type ListCRDsResp struct {
	CRDs [][2]string `json:"crds"`
}

type SkeletonReq struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
}
type SkeletonResp struct {
	YAML string `json:"yaml"`
}

type PruneReq struct {
	YAML string `json:"yaml"`
}
type PruneResp struct {
	YAML    string   `json:"yaml"`
	Removed []string `json:"removed"`
}

type ListSupReq struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
}
type ListSupResp struct {
	Paths []string `json:"paths"`
}

type ValReq struct {
	YAML string `json:"yaml"`
}
type ValResp struct {
	Valid   bool   `json:"valid"`
	Details string `json:"details"`
}

func (s *Service) ListCRDs() ListCRDsResp {
	return ListCRDsResp{CRDs: s.Disc.ListCRDs()}
}

func (s *Service) Skeleton(r SkeletonReq) (SkeletonResp, error) {
	if !s.Disc.Has(r.APIVersion, r.Kind) {
		return SkeletonResp{}, fmt.Errorf("Unknown apiVersion/kind")
	}
	y, err := s.Disc.Skeleton(r.APIVersion, r.Kind)
	if err != nil {
		return SkeletonResp{}, err
	}
	return SkeletonResp{YAML: y}, nil
}

func (s *Service) ListSupported(r ListSupReq) (ListSupResp, error) {
	if !s.Disc.Has(r.APIVersion, r.Kind) {
		return ListSupResp{}, fmt.Errorf("Unknown apiVersion/kind")
	}
	if a := s.Disc.AllowedSpec(r.APIVersion, r.Kind); a != nil {
		paths := make([]string, 0, len(a))
		for p := range a {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		return ListSupResp{Paths: paths}, nil
	}
	tl := s.Disc.TopAllowed(r.APIVersion, r.Kind)
	// Filter boilerplate fields
	delete(tl, "apiVersion")
	delete(tl, "kind")
	delete(tl, "metadata")
	paths := make([]string, 0, len(tl))
	for p := range tl {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return ListSupResp{Paths: paths}, nil
}

func (s *Service) Prune(r PruneReq) (PruneResp, error) {
	cleaned, removedSpec, removedMetaOrTop, err := s.pruneImpl(r.YAML)
	if err != nil {
		return PruneResp{}, err
	}
	removed := append(removedSpec, removedMetaOrTop...)
	if len(removed) > 0 {
		return PruneResp{}, &UnsupportedRemoved{Removed: removed}
	}
	return PruneResp{YAML: cleaned, Removed: []string{}}, nil
}

type UnsupportedRemoved struct{ Removed []string }

func (e *UnsupportedRemoved) Error() string {
	lines := make([]string, len(e.Removed))
	for i, r := range e.Removed {
		lines[i] = "- " + r
	}
	return "Unsupported fields stripped:\n" + strings.Join(lines, "\n")
}

func (s *Service) Validate(r ValReq) ValResp {
	// Parse YAML first
	var tmp any
	if err := yaml.Unmarshal([]byte(r.YAML), &tmp); err != nil {
		return ValResp{Valid: false, Details: fmt.Sprintf("Invalid YAML: %v", err)}
	}
	// Detect what prune would remove (but do not remove it)
	_, badSpec, badMetaOrTop, err := s.pruneImpl(r.YAML)
	if err != nil {
		// Unknown api/kind or parse error surfaced during prune
		return ValResp{Valid: false, Details: err.Error()}
	}
	removed := append(badSpec, badMetaOrTop...)
	if len(removed) > 0 {
		return ValResp{
			Valid:   false,
			Details: "Unsupported fields (local schema): " + strings.Join(removed, ", "),
		}
	}
	return ValResp{Valid: true, Details: "Local schema check passed (no cluster dry-run)."}
}

func (s *Service) RefreshDiscovery() (ok bool, count int, err error) {
	if err := s.Disc.Refresh(context.Background()); err != nil {
		return false, 0, err
	}
	return true, s.Disc.FullCount(), nil
}

// ------------------- internals: prune implementation -------------------

func (s *Service) pruneImpl(doc string) (cleaned string, removedSpec, removedMetaOrTop []string, err error) {
	var data any
	if err := yaml.Unmarshal([]byte(doc), &data); err != nil {
		return "", nil, nil, fmt.Errorf("Invalid YAML: %w", err)
	}
	m, ok := data.(map[string]any)
	if !ok {
		m = map[string]any{}
	}

	api, _ := m["apiVersion"].(string)
	kind, _ := m["kind"].(string)
	if !s.Disc.Has(api, kind) {
		return "", nil, nil, fmt.Errorf("%s/%s is not known to the control plane", api, kind)
	}

	// ----- prune spec.* against allow-list ------------------------------
	if a := s.Disc.AllowedSpec(api, kind); a != nil {
		var walk func(node any, dotted string)
		walk = func(node any, dotted string) {
			switch x := node.(type) {
			case map[string]any:
				for k := range x {
					here := dotted
					if here != "" {
						here += "."
					}
					here += k
					clean := discovery.StripIndices(here)
					if strings.HasPrefix(clean, "spec.") && !discovery.IsAllowed(a, clean) {
						removedSpec = append(removedSpec, clean)
						delete(x, k)
						continue
					}
					walk(x[k], here)
				}
			case []any:
				for i := range x {
					walk(x[i], fmt.Sprintf("%s[%d]", dotted, i))
				}
			}
		}
		walk(m, "")
	}

	// ----- prune metadata annotations/labels (except allow-listed) ------
	if meta, ok := m["metadata"].(map[string]any); ok {
		if ann, ok := meta["annotations"].(map[string]any); ok {
			for k := range ann {
				if _, keep := s.AllowedMetaAnnotations[k]; !keep {
					removedMetaOrTop = append(removedMetaOrTop, "metadata.annotations."+k)
					delete(ann, k)
				}
			}
			if len(ann) == 0 {
				delete(meta, "annotations")
			}
		}
		if lab, ok := meta["labels"].(map[string]any); ok {
			for k := range lab {
				if _, keep := s.AllowedMetaLabels[k]; !keep {
					removedMetaOrTop = append(removedMetaOrTop, "metadata.labels."+k)
					delete(lab, k)
				}
			}
			if len(lab) == 0 {
				delete(meta, "labels")
			}
		}
		if len(meta) == 0 {
			delete(m, "metadata")
		}
	}

	// ----- drop stray top-level keys using discovered props -------------
	allowedTop := s.Disc.TopAllowed(api, kind)
	always := map[string]struct{}{"apiVersion": {}, "kind": {}, "metadata": {}}
	for k := range m {
		if _, ok := allowedTop[k]; ok {
			continue
		}
		if _, ok := always[k]; ok {
			continue
		}
		removedMetaOrTop = append(removedMetaOrTop, k)
		delete(m, k)
	}

	out, err := yaml.Marshal(m)
	if err != nil {
		return "", nil, nil, err
	}
	return string(out), removedSpec, removedMetaOrTop, nil
}

// Exported error inspector (useful for stdio/http layers to map codes)
func IsUnsupportedRemoved(err error) (bool, []string) {
	var e *UnsupportedRemoved
	if errors.As(err, &e) {
		return true, e.Removed
	}
	return false, nil
}
