/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resourcetemplate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// managedByLabelValue is the app.kubernetes.io/managed-by value stamped on generated CRDs.
const managedByLabelValue = "gitops-promoter"

// CRDGenerator creates or updates the wrapped template CustomResourceDefinitions for the resource kinds
// listed in ControllerConfiguration.spec.kubernetesResourceCommitStatus.resources. It runs once at
// controller startup, before the manager starts, with uncached clients.
type CRDGenerator struct {
	// Client reads and writes CustomResourceDefinitions. It must not be a cache-backed client.
	Client client.Client
	// Discovery resolves resource names and serves the /openapi/v3 documents for built-in kinds.
	Discovery discovery.DiscoveryInterfaceWithContext
	Log       logr.Logger
}

// NewCRDGenerator builds a CRDGenerator with uncached clients for cfg.
func NewCRDGenerator(cfg *rest.Config, log logr.Logger) (*CRDGenerator, error) {
	c, err := client.New(cfg, client.Options{Scheme: utils.GetScheme()})
	if err != nil {
		return nil, fmt.Errorf("create client for template CRD generation: %w", err)
	}
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create discovery client for template CRD generation: %w", err)
	}
	return &CRDGenerator{Client: c, Discovery: discovery.ToDiscoveryInterfaceWithContext(dc), Log: log}, nil
}

// sourceKind is a resolved configured kind.
type sourceKind struct {
	gvk        schema.GroupVersionKind
	plural     string
	singular   string
	namespaced bool
}

// Ensure generates the wrapped template CRD for every configured kind. Kinds that cannot be resolved or
// whose CRD cannot be written are reported in the returned (joined) error; the remaining kinds are still
// processed so one misconfigured entry does not block the others.
func (g *CRDGenerator) Ensure(ctx context.Context, resources []promoterv1alpha1.ResourceGroupVersionKind) error {
	// Group versions of the same Group/Kind into one CRD; the first listed version becomes the storage version.
	type groupKindKey struct{ group, kind string }
	order := make([]groupKindKey, 0, len(resources))
	versions := map[groupKindKey][]string{}
	for _, r := range resources {
		k := groupKindKey{group: r.Group, kind: r.Kind}
		if _, seen := versions[k]; !seen {
			order = append(order, k)
		}
		versions[k] = append(versions[k], r.Version)
	}

	var errs []error
	for _, k := range order {
		if err := g.ensureOne(ctx, k.group, k.kind, versions[k]); err != nil {
			g.Log.Error(err, "failed to generate wrapped template CRD", "group", k.group, "kind", k.kind)
			errs = append(errs, fmt.Errorf("%s/%s: %w", k.group, k.kind, err))
		}
	}
	return errors.Join(errs...)
}

func (g *CRDGenerator) ensureOne(ctx context.Context, group, kind string, versionNames []string) error {
	var crdVersions []apiextensionsv1.CustomResourceDefinitionVersion
	var names apiextensionsv1.CustomResourceDefinitionNames
	for i, version := range versionNames {
		gvk := schema.GroupVersionKind{Group: group, Version: version, Kind: kind}
		src, err := g.resolve(ctx, gvk)
		if err != nil {
			return err
		}
		if !src.namespaced {
			return fmt.Errorf("%s is cluster-scoped; only namespaced kinds can be used as templates", gvk)
		}
		if i == 0 {
			names = apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   src.plural,
				Singular: src.singular,
				Kind:     src.gvk.Kind,
				ListKind: src.gvk.Kind + "List",
			}
		}
		props, err := g.schemaFor(ctx, src)
		if err != nil {
			g.Log.Error(err, "falling back to a permissive template schema", "gvk", gvk.String())
			props = PermissiveSchema()
		}
		crdVersions = append(crdVersions, apiextensionsv1.CustomResourceDefinitionVersion{
			Name:    version,
			Served:  true,
			Storage: i == 0,
			Schema:  &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: props},
		})
	}

	crd := buildCRD(group, names, crdVersions)
	if err := g.apply(ctx, crd); err == nil {
		return nil
	} else if !isSchemaRejection(err) {
		return err
	} else {
		g.Log.Error(err, "API server rejected the converted template schema; retrying with a permissive schema", "crd", crd.Name)
	}
	for i := range crd.Spec.Versions {
		crd.Spec.Versions[i].Schema = &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: PermissiveSchema()}
	}
	return g.apply(ctx, crd)
}

// isSchemaRejection reports whether err is an API server validation failure (as opposed to RBAC or transport
// errors), which is the only case where retrying with a permissive schema can help.
func isSchemaRejection(err error) bool {
	return k8serrors.IsInvalid(err) || k8serrors.IsBadRequest(err)
}

func buildCRD(group string, names apiextensionsv1.CustomResourceDefinitionNames, versions []apiextensionsv1.CustomResourceDefinitionVersion) *apiextensionsv1.CustomResourceDefinition {
	templateGroup := TemplateGroupFor(group)
	sourceGroup := group
	if sourceGroup == "" {
		sourceGroup = promoterv1alpha1.TemplateCoreGroup
	}
	return &apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{APIVersion: apiextensionsv1.SchemeGroupVersion.String(), Kind: "CustomResourceDefinition"},
		ObjectMeta: metav1.ObjectMeta{
			Name: names.Plural + "." + templateGroup,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":    managedByLabelValue,
				promoterv1alpha1.TemplateCRDLabel: "true",
			},
			Annotations: map[string]string{
				promoterv1alpha1.TemplateCRDSourceAnnotation: sourceGroup + "/" + names.Kind,
			},
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group:    templateGroup,
			Names:    names,
			Scope:    apiextensionsv1.NamespaceScoped,
			Versions: versions,
		},
	}
}

// apply creates or updates crd, validating with a dry run first so a rejected schema surfaces before any write.
func (g *CRDGenerator) apply(ctx context.Context, crd *apiextensionsv1.CustomResourceDefinition) error {
	existing := &apiextensionsv1.CustomResourceDefinition{}
	err := g.Client.Get(ctx, client.ObjectKey{Name: crd.Name}, existing)
	switch {
	case k8serrors.IsNotFound(err):
		if err := g.Client.Create(ctx, crd.DeepCopy(), client.DryRunAll, client.FieldOwner(constants.KubernetesResourceTemplateCRDFieldOwner)); err != nil {
			return fmt.Errorf("dry-run create of CRD %s: %w", crd.Name, err)
		}
		if err := g.Client.Create(ctx, crd, client.FieldOwner(constants.KubernetesResourceTemplateCRDFieldOwner)); err != nil {
			return fmt.Errorf("create CRD %s: %w", crd.Name, err)
		}
		g.Log.Info("created wrapped template CRD", "crd", crd.Name)
		return nil
	case err != nil:
		return fmt.Errorf("get CRD %s: %w", crd.Name, err)
	}

	updated := existing.DeepCopy()
	updated.Labels = mergeStringMaps(existing.Labels, crd.Labels)
	updated.Annotations = mergeStringMaps(existing.Annotations, crd.Annotations)
	updated.Spec.Group = crd.Spec.Group
	updated.Spec.Names = crd.Spec.Names
	updated.Spec.Scope = crd.Spec.Scope
	updated.Spec.Versions = mergeVersions(existing, crd.Spec.Versions)
	if err := g.Client.Update(ctx, updated.DeepCopy(), client.DryRunAll, client.FieldOwner(constants.KubernetesResourceTemplateCRDFieldOwner)); err != nil {
		return fmt.Errorf("dry-run update of CRD %s: %w", crd.Name, err)
	}
	if err := g.Client.Update(ctx, updated, client.FieldOwner(constants.KubernetesResourceTemplateCRDFieldOwner)); err != nil {
		return fmt.Errorf("update CRD %s: %w", crd.Name, err)
	}
	g.Log.Info("updated wrapped template CRD", "crd", crd.Name)
	return nil
}

// mergeVersions keeps versions the API server still lists in status.storedVersions but that are no longer
// configured, served but non-storage, so the update is not rejected and existing templates stay readable.
func mergeVersions(existing *apiextensionsv1.CustomResourceDefinition, desired []apiextensionsv1.CustomResourceDefinitionVersion) []apiextensionsv1.CustomResourceDefinitionVersion {
	out := append([]apiextensionsv1.CustomResourceDefinitionVersion{}, desired...)
	for _, stored := range existing.Status.StoredVersions {
		found := false
		for _, v := range desired {
			if v.Name == stored {
				found = true
				break
			}
		}
		if found {
			continue
		}
		for _, v := range existing.Spec.Versions {
			if v.Name == stored {
				keep := *v.DeepCopy()
				keep.Storage = false
				keep.Served = true
				out = append(out, keep)
			}
		}
	}
	return out
}

func mergeStringMaps(base, overlay map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

// resolve looks up the API resource for gvk through discovery.
func (g *CRDGenerator) resolve(ctx context.Context, gvk schema.GroupVersionKind) (sourceKind, error) {
	list, err := g.Discovery.ServerResourcesForGroupVersionWithContext(ctx, gvk.GroupVersion().String())
	if err != nil {
		return sourceKind{}, fmt.Errorf("discover resources for %s: %w", gvk.GroupVersion(), err)
	}
	for _, r := range list.APIResources {
		if strings.Contains(r.Name, "/") || r.Kind != gvk.Kind {
			continue
		}
		singular := r.SingularName
		if singular == "" {
			singular = strings.ToLower(r.Kind)
		}
		return sourceKind{gvk: gvk, plural: r.Name, singular: singular, namespaced: r.Namespaced}, nil
	}
	return sourceKind{}, fmt.Errorf("kind %s is not served by the API server", gvk)
}

// schemaFor returns the structural schema for src: from the source CRD when the kind is CRD-backed,
// otherwise from the /openapi/v3 document of its group version.
func (g *CRDGenerator) schemaFor(ctx context.Context, src sourceKind) (*apiextensionsv1.JSONSchemaProps, error) {
	if src.gvk.Group != "" {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		err := g.Client.Get(ctx, client.ObjectKey{Name: src.plural + "." + src.gvk.Group}, crd)
		switch {
		case err == nil:
			for _, v := range crd.Spec.Versions {
				if v.Name == src.gvk.Version && v.Schema != nil && v.Schema.OpenAPIV3Schema != nil {
					return StructuralSchemaFromCRD(v.Schema.OpenAPIV3Schema)
				}
			}
			return nil, fmt.Errorf("CRD %s has no schema for version %s", crd.Name, src.gvk.Version)
		case !k8serrors.IsNotFound(err) && !k8serrors.IsForbidden(err):
			return nil, fmt.Errorf("get source CRD for %s: %w", src.gvk, err)
		}
	}
	return g.schemaFromOpenAPI(ctx, src.gvk)
}

func (g *CRDGenerator) schemaFromOpenAPI(ctx context.Context, gvk schema.GroupVersionKind) (*apiextensionsv1.JSONSchemaProps, error) {
	paths, err := g.Discovery.OpenAPIV3WithContext(ctx).PathsWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("list OpenAPI v3 paths: %w", err)
	}
	path := "apis/" + gvk.Group + "/" + gvk.Version
	if gvk.Group == "" {
		path = "api/" + gvk.Version
	}
	gv, ok := paths[path]
	if !ok {
		return nil, fmt.Errorf("OpenAPI v3 document %s not found", path)
	}
	doc, err := gv.SchemaWithContext(ctx, "application/json")
	if err != nil {
		return nil, fmt.Errorf("fetch OpenAPI v3 document %s: %w", path, err)
	}
	return StructuralSchemaFromOpenAPIV3(doc, gvk)
}
