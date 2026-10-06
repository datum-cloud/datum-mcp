package api

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// internalMetadataFields are Kubernetes bookkeeping fields that carry no
// meaning for an agent and needlessly inflate every tool response.
//
// resourceVersion and uid are deliberately not included here: resourceVersion
// is the concurrency token an agent must round-trip from a 'get' into a later
// update/delete to use optimistic concurrency (see WriteOptions), and uid is
// how an agent references the object elsewhere (e.g. ownerReferences).
var internalMetadataFields = []string{
	"managedFields",
	"generation",
	"creationTimestamp",
	"selfLink",
}

// CleanObject returns a shallow copy of obj's fields with internal-only
// Kubernetes metadata stripped. spec, status, name, namespace, labels, and
// annotations are preserved in full.
func CleanObject(obj *unstructured.Unstructured) map[string]any {
	if obj == nil {
		return nil
	}
	cleaned := make(map[string]any, len(obj.Object))
	for k, v := range obj.Object {
		cleaned[k] = v
	}
	if md, ok := cleaned["metadata"].(map[string]any); ok {
		mdCopy := make(map[string]any, len(md))
		for k, v := range md {
			mdCopy[k] = v
		}
		for _, f := range internalMetadataFields {
			delete(mdCopy, f)
		}
		cleaned["metadata"] = mdCopy
	}
	return cleaned
}

// CleanList returns cleaned copies of every item in list, in order.
func CleanList(list *unstructured.UnstructuredList) []map[string]any {
	if list == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, CleanObject(&list.Items[i]))
	}
	return out
}
