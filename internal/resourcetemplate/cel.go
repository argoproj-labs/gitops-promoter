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
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/golang/groupcache/lru"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sjson "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/version"
	apiservercel "k8s.io/apiserver/pkg/cel"
	"k8s.io/apiserver/pkg/cel/common"
	"k8s.io/apiserver/pkg/cel/environment"
	"k8s.io/apiserver/pkg/cel/openapi"
	"k8s.io/apiserver/pkg/cel/openapi/resolver"
	"k8s.io/kube-openapi/pkg/validation/spec"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	viewv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/view/v1alpha1"
)

// VariableSet selects which variables an expression may use. Each set is a superset of the previous one.
type VariableSet int

const (
	// TemplateVariables are available to spec.template: Branch, Sha, ShortSha, ReportOn, Environment,
	// PromotionStrategy, and KubernetesResourceCommitStatus.
	TemplateVariables VariableSet = iota
	// PhaseVariables add Resource, typed from the created kind's OpenAPI schema when it can be resolved.
	PhaseVariables
	// ReportVariables add Phase, ResourceName, ResourceKind, ExpressionDescription, and ExpressionUrl for
	// spec.description and spec.url.
	ReportVariables
)

// DefaultMaxPrograms bounds the compiled-program cache.
const DefaultMaxPrograms = 4096

const (
	// KindSchemaTTL is how long a resolved kind schema is reused before it is fetched again through discovery.
	// It bounds how long a CRD schema change goes unnoticed by the type checks.
	KindSchemaTTL = time.Minute
	// KindSchemaErrorTTL is how long a failed resolution is remembered: short enough that a kind whose CRD is being
	// installed is retried promptly, long enough that repeated reconciles of a broken gate do not hammer discovery.
	KindSchemaErrorTTL = 5 * time.Second
)

// ErrNoSchemaResolver is returned by ResolveKindSchema when the Compiler was built without a resolver.
var ErrNoSchemaResolver = errors.New("no schema resolver is configured")

// promoterTypeDefinitions maps variable names to the OpenAPI definition each is typed from.
var promoterTypeDefinitions = map[string]struct {
	definition string
	root       bool
}{
	"Environment":                    {definition: promoterv1alpha1.EnvironmentStatus{}.OpenAPIModelName()},
	"PromotionStrategy":              {definition: promoterv1alpha1.PromotionStrategy{}.OpenAPIModelName(), root: true},
	"KubernetesResourceCommitStatus": {definition: promoterv1alpha1.KubernetesResourceCommitStatus{}.OpenAPIModelName(), root: true},
}

var templateStringVariables = []string{"Branch", "Sha", "ShortSha", "ReportOn"}

var reportStringVariables = []string{"Phase", "ResourceName", "ResourceKind", "ExpressionDescription", "ExpressionUrl"}

// KindSchema is the resolved OpenAPI schema of a kind. Decl is the CEL declaration that types the Resource variable
// and the fields of spec.template for static checks; Schema is the source schema with apiVersion, kind, and metadata
// added at the root, which keeps the information the declaration drops (for example whether an object preserves
// unknown fields).
type KindSchema struct {
	Decl   *apiservercel.DeclType
	Schema *spec.Schema
	hash   string
}

func (ks *KindSchema) hashOrEmpty() string {
	if ks == nil {
		return ""
	}
	return ks.hash
}

type kindSchemaEntry struct {
	schema  *KindSchema
	err     error
	expires time.Time
}

// Compiler builds typed CEL environments for KubernetesResourceCommitStatus expressions and caches the compiled
// programs. The environment is the Kubernetes base environment used by CRD validation rules and admission
// policies (optional types, the Kubernetes extension libraries, and per-call cost limits). Promoter variables are
// typed from the generated OpenAPI definitions, and Resource is typed from the created kind's schema when a
// resolver is configured and the schema can be found; otherwise it is dyn.
//
// Kind schemas are re-resolved after KindSchemaTTL (KindSchemaErrorTTL after a failure). When a schema changes,
// the environments built from the previous one are dropped and their programs age out of the cache.
type Compiler struct {
	resolver      resolver.SchemaResolver
	promoterTypes map[string]*apiservercel.DeclType
	kindSchemas   map[schema.GroupVersionKind]kindSchemaEntry
	envs          map[envKey]*cel.Env
	programs      *lru.Cache
	now           func() time.Time
	log           logr.Logger
	mu            sync.Mutex
}

type envKey struct {
	gvk  schema.GroupVersionKind
	hash string
	set  VariableSet
}

type programKey struct {
	expression string
	envKey
}

// Program is a compiled expression with its statically inferred output type.
type Program struct {
	program    cel.Program
	OutputType *cel.Type
}

// NewCompiler returns a Compiler. schemaResolver may be nil, in which case Resource is untyped.
func NewCompiler(schemaResolver resolver.SchemaResolver, log logr.Logger) (*Compiler, error) {
	promoterTypes, err := promoterDeclTypes()
	if err != nil {
		return nil, err
	}
	return &Compiler{
		resolver:      schemaResolver,
		promoterTypes: promoterTypes,
		kindSchemas:   map[schema.GroupVersionKind]kindSchemaEntry{},
		envs:          map[envKey]*cel.Env{},
		programs:      lru.New(DefaultMaxPrograms),
		now:           time.Now,
		log:           log,
	}, nil
}

// promoterDeclTypes builds the CEL declarations for the Promoter-typed variables from the generated OpenAPI
// definitions of the view API, which embeds the promoter v1alpha1 types.
func promoterDeclTypes() (map[string]*apiservercel.DeclType, error) {
	defs := viewv1alpha1.GetOpenAPIDefinitions(spec.MustCreateRef)
	schemaOf := func(name string) (*spec.Schema, bool) {
		def, ok := defs[name]
		if !ok {
			return nil, false
		}
		s := def.Schema
		return &s, true
	}
	out := make(map[string]*apiservercel.DeclType, len(promoterTypeDefinitions))
	for name, td := range promoterTypeDefinitions {
		s, err := resolver.PopulateRefs(schemaOf, td.definition)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve OpenAPI definition for %s: %w", name, err)
		}
		if td.root {
			s = common.WithTypeAndObjectMeta(s)
		}
		t := common.SchemaDeclType(&openapi.Schema{Schema: s}, td.root)
		if t == nil {
			return nil, fmt.Errorf("OpenAPI definition for %s has no CEL declaration", name)
		}
		out[name] = t.MaybeAssignTypeName("promoter." + name)
	}
	return out, nil
}

// ResolveKindSchema returns the schema of gvk, from cache when it was resolved within KindSchemaTTL. Errors from
// the resolver are wrapped; errors.Is(err, resolver.ErrSchemaNotFound) means the cluster does not serve the kind.
func (c *Compiler) ResolveKindSchema(gvk schema.GroupVersionKind) (*KindSchema, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resolveKindSchemaLocked(gvk)
}

func (c *Compiler) resolveKindSchemaLocked(gvk schema.GroupVersionKind) (*KindSchema, error) {
	if c.resolver == nil {
		return nil, ErrNoSchemaResolver
	}
	now := c.now()
	prev, cached := c.kindSchemas[gvk]
	if cached && now.Before(prev.expires) {
		return prev.schema, prev.err
	}
	ks, err := c.buildKindSchema(gvk, prev.schema)
	if err != nil {
		err = fmt.Errorf("failed to resolve the OpenAPI schema of %s: %w", gvk, err)
		c.log.Info("could not resolve the OpenAPI schema for a resource kind; its expressions are type-checked dynamically", "gvk", gvk.String(), "error", err.Error())
		c.kindSchemas[gvk] = kindSchemaEntry{err: err, expires: now.Add(KindSchemaErrorTTL)}
		return nil, err
	}
	if prev.schema != nil && prev.schema.hash != ks.hash {
		// Environments typed from the previous schema are stale; their programs age out of the LRU.
		for key := range c.envs {
			if key.gvk == gvk && key.hash != ks.hash {
				delete(c.envs, key)
			}
		}
	}
	c.kindSchemas[gvk] = kindSchemaEntry{schema: ks, expires: now.Add(KindSchemaTTL)}
	return ks, nil
}

// buildKindSchema resolves gvk and converts it; prev is returned unchanged when the schema has not changed, so
// callers can detect changes by pointer identity or hash.
func (c *Compiler) buildKindSchema(gvk schema.GroupVersionKind, prev *KindSchema) (*KindSchema, error) {
	s, err := c.resolver.ResolveSchema(gvk)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller adds the kind
	}
	full := common.WithTypeAndObjectMeta(s)
	markOpenSchemasDynamic(full)
	raw, err := json.Marshal(full)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the schema: %w", err)
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	if prev != nil && prev.hash == hash {
		return prev, nil
	}
	decl := common.SchemaDeclType(&openapi.Schema{Schema: full}, true)
	if decl == nil {
		return nil, errors.New("the schema has no CEL declaration")
	}
	decl = decl.MaybeAssignTypeName(strings.ReplaceAll(gvk.String(), " ", "") + ".Resource")
	return &KindSchema{Decl: decl, Schema: full, hash: hash}, nil
}

// markOpenSchemasDynamic makes every subschema that preserves unknown fields, or declares no type at all, a
// dynamically typed value in CEL by tagging it x-kubernetes-int-or-string (the one shape SchemaDeclType maps to dyn).
// The API server's own conversion exposes such objects as closed structs and drops untyped fields entirely, which is
// right for validation rules over a CRD's own fields but wrong here: Tekton's params[].value and results[].value,
// for example, are untyped unions, and Resource.status.results[0].value must compile. Declared properties inside an
// open object lose their static types; the template walk treats the same schemas as accepting any key.
func markOpenSchemasDynamic(s *spec.Schema) {
	if s == nil {
		return
	}
	for i := range s.AllOf {
		markOpenSchemasDynamic(&s.AllOf[i])
	}
	for i := range s.AnyOf {
		markOpenSchemasDynamic(&s.AnyOf[i])
	}
	for i := range s.OneOf {
		markOpenSchemasDynamic(&s.OneOf[i])
	}
	for name, prop := range s.Properties {
		markOpenSchemasDynamic(&prop)
		s.Properties[name] = prop
	}
	if s.Items != nil {
		markOpenSchemasDynamic(s.Items.Schema)
		for i := range s.Items.Schemas {
			markOpenSchemasDynamic(&s.Items.Schemas[i])
		}
	}
	if s.AdditionalProperties != nil {
		markOpenSchemasDynamic(s.AdditionalProperties.Schema)
	}
	preserve, _ := s.Extensions.GetBool("x-kubernetes-preserve-unknown-fields")
	untyped := len(s.Type) == 0 && len(s.Properties) == 0 && s.Items == nil && s.AdditionalProperties == nil &&
		len(s.AllOf)+len(s.AnyOf)+len(s.OneOf) == 0
	if preserve || untyped {
		s.AddExtension("x-kubernetes-int-or-string", true)
	}
}

// ResourceType returns the CEL declaration of gvk from its OpenAPI schema, or nil when no resolver is configured
// or the schema cannot be resolved. Callers that need to know why use ResolveKindSchema.
func (c *Compiler) ResourceType(gvk schema.GroupVersionKind) *apiservercel.DeclType {
	ks, err := c.ResolveKindSchema(gvk)
	if err != nil {
		return nil
	}
	return ks.Decl
}

func (c *Compiler) env(key envKey, ks *KindSchema) (*cel.Env, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if env, ok := c.envs[key]; ok {
		return env, nil
	}
	var opts []cel.EnvOption
	var declTypes []*apiservercel.DeclType
	for _, name := range templateStringVariables {
		opts = append(opts, cel.Variable(name, cel.StringType))
	}
	for name, t := range c.promoterTypes {
		declTypes = append(declTypes, t)
		opts = append(opts, cel.Variable(name, t.CelType()))
	}
	if key.set >= PhaseVariables {
		if ks != nil {
			declTypes = append(declTypes, ks.Decl)
			opts = append(opts, cel.Variable("Resource", ks.Decl.CelType()))
		} else {
			opts = append(opts, cel.Variable("Resource", cel.DynType))
		}
	}
	if key.set >= ReportVariables {
		for _, name := range reportStringVariables {
			opts = append(opts, cel.Variable(name, cel.StringType))
		}
	}
	envSet, err := environment.MustBaseEnvSet(environment.DefaultCompatibilityVersion()).Extend(environment.VersionedOptions{
		IntroducedVersion: version.MajorMinor(1, 0),
		EnvOptions:        opts,
		DeclTypes:         declTypes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build CEL environment: %w", err)
	}
	env := envSet.NewExpressionsEnv()
	c.envs[key] = env
	return env, nil
}

// Compile returns the compiled program for expression under the given variable set, from cache when possible.
// gvk selects the Resource type and is ignored for TemplateVariables. A kind whose schema cannot be resolved
// leaves Resource untyped.
func (c *Compiler) Compile(set VariableSet, gvk schema.GroupVersionKind, expression string) (*Program, error) {
	var ks *KindSchema
	if set == TemplateVariables {
		gvk = schema.GroupVersionKind{}
	} else {
		ks, _ = c.ResolveKindSchema(gvk)
	}
	key := programKey{envKey: envKey{gvk: gvk, set: set, hash: ks.hashOrEmpty()}, expression: expression}
	c.mu.Lock()
	cached, ok := c.programs.Get(key)
	c.mu.Unlock()
	if ok {
		if p, ok := cached.(*Program); ok {
			return p, nil
		}
	}
	env, err := c.env(key.envKey, ks)
	if err != nil {
		return nil, err
	}
	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("expression %q: %w", strings.TrimSpace(expression), issues.Err())
	}
	program, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("expression %q: %w", strings.TrimSpace(expression), err)
	}
	p := &Program{program: program, OutputType: ast.OutputType()}
	c.mu.Lock()
	c.programs.Add(key, p)
	c.mu.Unlock()
	return p, nil
}

// Eval runs the program against activation and returns the result as a JSON-compatible Go value (nil, bool,
// int64, float64, string, []any, map[string]any). Optional values unwrap; an absent optional is nil.
func (p *Program) Eval(activation map[string]any) (any, error) {
	out, _, err := p.program.Eval(activation)
	if err != nil {
		return nil, err //nolint:wrapcheck // callers add the expression
	}
	native, err := celToNative(out)
	if err != nil {
		return nil, err
	}
	return normalizeJSON(native)
}

// celToNative converts a CEL value to plain Go values.
func celToNative(val ref.Val) (any, error) {
	if opt, ok := val.(*types.Optional); ok {
		if !opt.HasValue() {
			return nil, nil
		}
		return celToNative(opt.GetValue())
	}
	switch v := val.(type) {
	case types.Null:
		return nil, nil
	case types.Bool:
		return bool(v), nil
	case types.Int:
		return int64(v), nil
	case types.Uint:
		return uint64(v), nil
	case types.Double:
		return float64(v), nil
	case types.String:
		return string(v), nil
	case types.Bytes:
		return base64.StdEncoding.EncodeToString([]byte(v)), nil
	case types.Timestamp:
		return v.Time.UTC().Format(time.RFC3339Nano), nil
	case types.Duration:
		return v.String(), nil
	case *types.Err:
		return nil, v
	}
	if m, ok := val.(traits.Mapper); ok {
		out := map[string]any{}
		it := m.Iterator()
		for it.HasNext() == types.True {
			k := it.Next()
			ks, ok := k.Value().(string)
			if !ok {
				return nil, fmt.Errorf("map key %v is %s, only string keys can be rendered", k.Value(), k.Type().TypeName())
			}
			v, err := celToNative(m.Get(k))
			if err != nil {
				return nil, err
			}
			out[ks] = v
		}
		return out, nil
	}
	if l, ok := val.(traits.Lister); ok {
		out := []any{}
		it := l.Iterator()
		for it.HasNext() == types.True {
			v, err := celToNative(it.Next())
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	return val.Value(), nil
}

// normalizeJSON converts v into the value set unstructured objects use through a JSON round-trip: typed structs
// become maps keyed by their JSON names, integers become int64, and floats float64.
func normalizeJSON(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("expression result of type %T cannot be encoded as JSON: %w", v, err)
	}
	var out any
	if err := k8sjson.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("failed to decode expression result: %w", err)
	}
	return out, nil
}

// ToActivationObject converts a typed API object (or any JSON-serializable value) into the map form CEL
// expressions read, keyed by JSON field names. A nil pointer converts to nil.
func ToActivationObject(obj any) (map[string]any, error) {
	v, err := normalizeJSON(obj)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected an object, got %T", v)
	}
	return m, nil
}

// CheckOutputType reports whether a program whose output type is out can be assigned to a field declared as
// expected. A nil or dyn expected type accepts anything; a null or dyn output is accepted everywhere (null drops
// the field at render time), and an optional output is checked by its value type (an absent optional drops the
// field too). Object and map fields accept any map, list fields any list, and double fields accept integers.
func CheckOutputType(out *cel.Type, expected *apiservercel.DeclType) error {
	if expected == nil || out == nil {
		return nil
	}
	if out.Kind() == types.OpaqueKind && out.TypeName() == "optional_type" && len(out.Parameters()) == 1 {
		return CheckOutputType(out.Parameters()[0], expected)
	}
	exp := expected.CelType()
	if exp.Kind() == types.DynKind || exp.Kind() == types.AnyKind || out.Kind() == types.DynKind || out.Kind() == types.NullTypeKind {
		return nil
	}
	var ok bool
	switch {
	case expected.IsObject(), expected.IsMap():
		ok = out.Kind() == types.MapKind || out.Kind() == types.StructKind
	case expected.IsList():
		ok = out.Kind() == types.ListKind
	case exp.Kind() == types.DoubleKind:
		ok = out.Kind() == types.DoubleKind || out.Kind() == types.IntKind || out.Kind() == types.UintKind
	default:
		ok = exp.IsAssignableType(out)
	}
	if !ok {
		return fmt.Errorf("returns %s but the field expects %s", out.TypeName(), describeDeclType(expected))
	}
	return nil
}

func describeDeclType(t *apiservercel.DeclType) string {
	switch {
	case t.IsObject():
		return "an object"
	case t.IsMap():
		return "a map"
	case t.IsList():
		return "a list"
	default:
		return t.CelType().TypeName()
	}
}

// FieldType descends from parent to the declaration of key (an object field or map value). It returns nil when
// the type is unknown, which disables static checking below that point.
func FieldType(parent *apiservercel.DeclType, key string) *apiservercel.DeclType {
	if parent == nil {
		return nil
	}
	switch {
	case parent.IsObject():
		if f, ok := parent.FindField(key); ok {
			return f.Type
		}
		return nil
	case parent.IsMap():
		return parent.ElemType
	default:
		return nil
	}
}

// ElemType returns the declaration of a list's items, or nil when parent is not a known list.
func ElemType(parent *apiservercel.DeclType) *apiservercel.DeclType {
	if parent == nil || !parent.IsList() {
		return nil
	}
	return parent.ElemType
}
