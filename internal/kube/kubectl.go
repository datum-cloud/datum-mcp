package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Kubectl struct {
	Path      string   // default: "kubectl"
	Context   string   // optional --context
	Namespace string   // optional -n
	Env       []string // defaults to os.Environ()
}

func New() *Kubectl { return &Kubectl{Path: "kubectl", Env: os.Environ()} }

func (k *Kubectl) args(base ...string) []string {
	args := make([]string, 0, len(base)+4)
	if k.Context != "" {
		args = append(args, "--context", k.Context)
	}
	if k.Namespace != "" {
		args = append(args, "-n", k.Namespace)
	}
	args = append(args, base...)
	return args
}

func (k *Kubectl) run(ctx context.Context, stdin []byte, base ...string) ([]byte, []byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, k.Path, k.args(base...)...)
	cmd.Env = k.Env
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, err bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &err
	runErr := cmd.Run()
	return out.Bytes(), err.Bytes(), runErr
}

// ---------------- high-level operations ----------------

type CRDItem struct {
	Name     string   `json:"name"`     // metadata.name, e.g. httpproxies.networking.example.io
	Group    string   `json:"group"`    // spec.group
	Kind     string   `json:"kind"`     // spec.names.kind
	Versions []string `json:"versions"` // served versions
}

func (k *Kubectl) ListCRDs(ctx context.Context) ([]CRDItem, error) {
	stdout, stderr, err := k.run(ctx, nil, "get", "crd", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("kubectl get crd: %v: %s", err, strings.TrimSpace(string(stderr)))
	}
	var payload struct {
		Items []struct {
			Metadata struct{ Name string `json:"name"` } `json:"metadata"`
			Spec     struct {
				Group    string `json:"group"`
				Names    struct{ Kind string `json:"kind"` } `json:"names"`
				Versions []struct {
					Name   string `json:"name"`
					Served bool   `json:"served"`
				} `json:"versions"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout, &payload); err != nil {
		return nil, fmt.Errorf("parse CRDs json: %w", err)
	}
	out := make([]CRDItem, 0, len(payload.Items))
	for _, it := range payload.Items {
		var vs []string
		for _, v := range it.Spec.Versions {
			if v.Served {
				vs = append(vs, v.Name)
			}
		}
		out = append(out, CRDItem{
			Name:     it.Metadata.Name,
			Group:    it.Spec.Group,
			Kind:     it.Spec.Names.Kind,
			Versions: vs,
		})
	}
	return out, nil
}

func (k *Kubectl) GetCRD(ctx context.Context, name, mode string) (string, error) {
	switch mode {
	case "", "yaml", "json":
		if mode == "" {
			mode = "yaml"
		}
		stdout, stderr, err := k.run(ctx, nil, "get", "crd", name, "-o", mode)
		if err != nil {
			return "", fmt.Errorf("kubectl get crd %s: %v: %s", name, err, strings.TrimSpace(string(stderr)))
		}
		return string(stdout), nil
	case "describe":
		stdout, stderr, err := k.run(ctx, nil, "describe", "crd", name)
		if err != nil {
			return "", fmt.Errorf("kubectl describe crd %s: %v: %s", name, err, strings.TrimSpace(string(stderr)))
		}
		return string(stdout), nil
	default:
		return "", fmt.Errorf("unsupported mode %q (use yaml|json|describe)", mode)
	}
}

func (k *Kubectl) ValidateYAML(ctx context.Context, manifest string) (ok bool, output string, err error) {
	args := []string{"apply", "--dry-run=server", "--validate=true", "-f", "-"}
	stdout, stderr, runErr := k.run(ctx, []byte(manifest), args...)
	out := strings.TrimSpace(string(stdout))
	errs := strings.TrimSpace(string(stderr))
	if runErr != nil {
		// API server returns non-zero for validation failures; surface combined output
		if out != "" && errs != "" {
			return false, out + "\n" + errs, nil
		}
		if out != "" {
			return false, out, nil
		}
		return false, errs, nil
	}
	return true, out, nil
}
