package api

import (
	"fmt"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DefaultListLimit is applied when a caller doesn't specify one.
	DefaultListLimit = 100
	// MaxListLimit bounds every list call regardless of what a caller asks
	// for, so a single tool call can't dump an unbounded collection into
	// model context. Callers that want more follow the continue token.
	MaxListLimit = 500
)

// ListOptions bounds and filters a FetchList call. LabelSelector and
// FieldSelector use the same string syntax as kubectl (e.g. "team=edge",
// "metadata.name=foo").
type ListOptions struct {
	Limit         int64
	Continue      string
	LabelSelector string
	FieldSelector string
}

// normalizedLimit clamps Limit into [1, MaxListLimit], defaulting to
// DefaultListLimit when unset.
func (o ListOptions) normalizedLimit() int64 {
	switch {
	case o.Limit <= 0:
		return DefaultListLimit
	case o.Limit > MaxListLimit:
		return MaxListLimit
	default:
		return o.Limit
	}
}

// toListOpts converts ListOptions into controller-runtime ListOptions,
// returning an error if a selector fails to parse.
func (o ListOptions) toListOpts() ([]ctrlclient.ListOption, error) {
	opts := []ctrlclient.ListOption{ctrlclient.Limit(o.normalizedLimit())}
	if o.Continue != "" {
		opts = append(opts, ctrlclient.Continue(o.Continue))
	}
	if o.LabelSelector != "" {
		sel, err := labels.Parse(o.LabelSelector)
		if err != nil {
			return nil, fmt.Errorf("invalid labelSelector %q: %w", o.LabelSelector, err)
		}
		opts = append(opts, ctrlclient.MatchingLabelsSelector{Selector: sel})
	}
	if o.FieldSelector != "" {
		sel, err := fields.ParseSelector(o.FieldSelector)
		if err != nil {
			return nil, fmt.Errorf("invalid fieldSelector %q: %w", o.FieldSelector, err)
		}
		opts = append(opts, ctrlclient.MatchingFieldsSelector{Selector: sel})
	}
	return opts, nil
}

// WriteOptions controls create/update/delete behavior.
type WriteOptions struct {
	// DryRun asks the server to validate and run admission without
	// persisting the change. Standard Kubernetes apiserver behavior; not
	// guaranteed for every custom controller's reconciliation path.
	DryRun bool
	// ExpectedResourceVersion, for update/delete only: when non-empty, the
	// operation fails with a *ConflictError instead of proceeding if the
	// resource's current resourceVersion doesn't match. Empty means no
	// check (last-write-wins), which is the pre-existing behavior.
	ExpectedResourceVersion string
}

func (w WriteOptions) createOpts() []ctrlclient.CreateOption {
	if w.DryRun {
		return []ctrlclient.CreateOption{ctrlclient.DryRunAll}
	}
	return nil
}

func (w WriteOptions) updateOpts() []ctrlclient.UpdateOption {
	if w.DryRun {
		return []ctrlclient.UpdateOption{ctrlclient.DryRunAll}
	}
	return nil
}

func (w WriteOptions) deleteOpts() []ctrlclient.DeleteOption {
	var opts []ctrlclient.DeleteOption
	if w.DryRun {
		opts = append(opts, ctrlclient.DryRunAll)
	}
	if w.ExpectedResourceVersion != "" {
		rv := w.ExpectedResourceVersion
		opts = append(opts, ctrlclient.Preconditions{ResourceVersion: &rv})
	}
	return opts
}

// ConflictError is returned by UpdateObjectSpec when a caller supplies
// ExpectedResourceVersion and the resource has since changed.
type ConflictError struct {
	Kind string
	Name string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s %q was modified since that resourceVersion was read; re-fetch with action=get and retry", e.Kind, e.Name)
}
