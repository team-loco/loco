// Package varref parses and resolves the references in a resource's variables: ${{ secrets.NAME }}
// names a secret of the environment, ${{ resource.KEY }} an output of another resource in it.
package varref

import (
	"errors"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/internal/rpcerr"
)

// Secrets is the source a reference to a secret of the environment names.
const Secrets = "secrets"

// Built-in outputs every resource provides.
const (
	PrivateHost = "PRIVATE_HOST"
	Port        = "PORT"
	PrivateURL  = "PRIVATE_URL"
	PublicURL   = "PUBLIC_URL"
)

var (
	ErrMalformed = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"reference is not of the form ${{ secrets.NAME }} or ${{ resource.NAME }}",
	)
	ErrUnknownSecret = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"reference names a secret the environment does not have",
	)
	ErrUnknownResource = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"reference names a resource the workspace does not have",
	)
	ErrUnknownOutput = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"reference names a variable the resource does not provide",
	)
	ErrCycle = rpcerr.NewCoded(connect.CodeInvalidArgument, "references form a cycle")
)

var (
	refPattern = regexp.MustCompile(`\$\{\{(.*?)\}\}`)
	refInner   = regexp.MustCompile(`^\s*([a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?)\.([A-Za-z_][A-Za-z0-9_]*)\s*$`)
)

// Ref is one reference in a value.
type Ref struct {
	Source string
	Key    string
}

// IsSecret reports whether the reference names a secret.
func (r Ref) IsSecret() bool {
	return r.Source == Secrets
}

// String is the reference in its canonical form.
func (r Ref) String() string {
	return "${{ " + r.Source + "." + r.Key + " }}"
}

// Parse returns the references in value in order.
func Parse(value string) ([]Ref, error) {
	var refs []Ref
	if _, err := Render(value, func(ref Ref) (string, error) {
		refs = append(refs, ref)
		return "", nil
	}); err != nil {
		return nil, err
	}
	return refs, nil
}

// Render replaces every reference in value with what lookup returns for it.
func Render(value string, lookup func(Ref) (string, error)) (string, error) {
	var b strings.Builder
	last := 0
	for _, loc := range refPattern.FindAllStringSubmatchIndex(value, -1) {
		parts := refInner.FindStringSubmatch(value[loc[2]:loc[3]])
		if parts == nil {
			return "", ErrMalformed.Detail("%s", value[loc[0]:loc[1]])
		}
		replacement, err := lookup(Ref{Source: parts[1], Key: parts[2]})
		if err != nil {
			return "", err
		}
		b.WriteString(value[last:loc[0]])
		b.WriteString(replacement)
		last = loc[1]
	}
	b.WriteString(value[last:])
	return b.String(), nil
}

// Target is what a resource in the environment provides to references.
type Target struct {
	// Port is the port it listens on, 0 for none.
	Port int32
	// PublicHost is its primary domain, "" for none.
	PublicHost string
	// Env is its variables as written.
	Env map[string]string
}

// Output is one built-in output of a resource.
type Output struct {
	Key   string
	Value string
}

// Outputs lists the built-in outputs of the resource named name.
func Outputs(name string, t Target) []Output {
	outputs := []Output{{Key: PrivateHost, Value: name}}
	if t.Port > 0 {
		port := strconv.Itoa(int(t.Port))
		outputs = append(outputs,
			Output{Key: Port, Value: port},
			Output{Key: PrivateURL, Value: "http://" + name + ":" + port},
		)
	}
	if t.PublicHost != "" {
		outputs = append(outputs, Output{Key: PublicURL, Value: "https://" + t.PublicHost})
	}
	return outputs
}

// Environment is what the references of one environment resolve against.
type Environment struct {
	// Secrets holds the names of the environment's secrets.
	Secrets map[string]bool
	// Resources holds the workspace's resources by name.
	Resources map[string]Target
}

// Resolved is a resource's variables with every reference to another resource replaced.
type Resolved struct {
	// Plain holds the variables that reference no secret, fully resolved.
	Plain map[string]string
	// Secret holds the variables that reference a secret, with only secret references left.
	Secret map[string]string
	// Resources lists the resources the variables reference directly, sorted.
	Resources []string
}

// ReferencedResources lists the resources a set of variables refers to, sorted.
func ReferencedResources(env map[string]string) []string {
	seen := map[string]bool{}
	for _, value := range env {
		refs, err := Parse(value)
		if err != nil {
			continue
		}
		for _, ref := range refs {
			if !ref.IsSecret() {
				seen[ref.Source] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// SecretNames lists the secrets a set of secret-bearing variables reads, sorted.
func SecretNames(secretEnv map[string]string) []string {
	seen := map[string]bool{}
	for _, value := range secretEnv {
		refs, err := Parse(value)
		if err != nil {
			continue
		}
		for _, ref := range refs {
			if ref.IsSecret() {
				seen[ref.Key] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// SecretBytes is the size of the Secret that carries secretEnv: each variable's name and its value
// once every reference is replaced by the secret of that name, whose size sizes holds.
func SecretBytes(secretEnv map[string]string, sizes map[string]int64) int64 {
	var total int64
	for name, value := range secretEnv {
		total += int64(len(name) + len(value))
		for _, match := range refPattern.FindAllStringSubmatch(value, -1) {
			if parts := refInner.FindStringSubmatch(match[1]); parts != nil {
				total += sizes[parts[2]] - int64(len(match[0]))
			}
		}
	}
	return total
}

// VariableError is why one variable does not resolve.
type VariableError struct {
	Name string
	Err  error
}

func (e *VariableError) Error() string {
	return e.Err.Error()
}

func (e *VariableError) Unwrap() error {
	return e.Err
}

// Resolve resolves the variables env of the resource named self, which replace the variables
// the environment records for it. The error joins one *VariableError per failing variable, in
// name order.
func (e Environment) Resolve(self string, target Target, env map[string]string) (Resolved, error) {
	resources := maps.Clone(e.Resources)
	if resources == nil {
		resources = map[string]Target{}
	}
	target.Env = env
	resources[self] = target
	r := &resolver{env: Environment{Secrets: e.Secrets, Resources: resources}, done: map[node]string{}}

	resolved := Resolved{Plain: map[string]string{}, Secret: map[string]string{}}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(env)) {
		r.name = name
		value, err := r.variable(node{resource: self, key: name})
		if err != nil {
			errs = append(errs, &VariableError{Name: name, Err: err})
			continue
		}
		if hasSecret(value) {
			resolved.Secret[name] = value
		} else {
			resolved.Plain[name] = value
		}
	}
	if len(errs) > 0 {
		return Resolved{}, errors.Join(errs...)
	}
	resolved.Resources = slices.DeleteFunc(ReferencedResources(env), func(name string) bool { return name == self })
	return resolved, nil
}

// VariableErrors splits an error Resolve returned into its variables' errors.
func VariableErrors(err error) []*VariableError {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		if variable, isVariable := errors.AsType[*VariableError](err); isVariable {
			return []*VariableError{variable}
		}
		return nil
	}
	var out []*VariableError
	for _, inner := range joined.Unwrap() {
		if variable, isVariable := errors.AsType[*VariableError](inner); isVariable {
			out = append(out, variable)
		}
	}
	return out
}

func hasSecret(value string) bool {
	refs, err := Parse(value)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(refs, Ref.IsSecret)
}

type node struct {
	resource string
	key      string
}

func (n node) String() string {
	return n.resource + "." + n.key
}

type resolver struct {
	env  Environment
	done map[node]string
	path []node
	// name is the variable of the deployed resource being resolved.
	name string
}

// variable resolves the variable n of a resource, leaving its secret references in place.
func (r *resolver) variable(n node) (string, error) {
	if value, ok := r.done[n]; ok {
		return value, nil
	}
	if i := slices.Index(r.path, n); i >= 0 {
		chain := make([]string, 0, len(r.path)-i+1)
		for _, step := range r.path[i:] {
			chain = append(chain, step.String())
		}
		return "", ErrCycle.Detail("%s: %s", r.name, strings.Join(append(chain, n.String()), " → "))
	}
	raw := r.env.Resources[n.resource].Env[n.key]
	r.path = append(r.path, n)
	value, err := Render(raw, r.reference)
	if errors.Is(err, ErrMalformed) {
		err = ErrMalformed.Detail("%s: %s", r.name, raw)
	}
	r.path = r.path[:len(r.path)-1]
	if err != nil {
		return "", err
	}
	r.done[n] = value
	return value, nil
}

func (r *resolver) reference(ref Ref) (string, error) {
	if ref.IsSecret() {
		if !r.env.Secrets[ref.Key] {
			return "", ErrUnknownSecret.Detail("%s refers to %s", r.name, ref)
		}
		return ref.String(), nil
	}
	target, ok := r.env.Resources[ref.Source]
	if !ok {
		return "", ErrUnknownResource.Detail("%s refers to %s", r.name, ref)
	}
	for _, output := range Outputs(ref.Source, target) {
		if output.Key == ref.Key {
			return output.Value, nil
		}
	}
	if _, ok = target.Env[ref.Key]; !ok {
		return "", ErrUnknownOutput.Detail("%s refers to %s", r.name, ref)
	}
	return r.variable(node{resource: ref.Source, key: ref.Key})
}
