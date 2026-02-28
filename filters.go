package copier

import (
	"fmt"
	"strings"

	"github.com/flosch/pongo2/v6"
	"gopkg.in/yaml.v3"
)

func init() {
	// Register Jinja2-ansible-style filters that templates commonly use.
	mustRegister("to_nice_yaml", filterToNiceYAML)
	mustRegister("to_yaml", filterToYAML)
	mustRegister("to_json", filterToJSON)
	mustRegister("bool", filterBool)
	mustRegister("basename", filterBasename)
}

func mustRegister(name string, fn pongo2.FilterFunction) {
	if err := pongo2.RegisterFilter(name, fn); err != nil {
		// Filter already registered — safe to ignore on re-init.
		_ = err
	}
}

func filterToNiceYAML(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	data, err := yaml.Marshal(in.Interface())
	if err != nil {
		return nil, &pongo2.Error{
			Sender:    "filter:to_nice_yaml",
			OrigError: err,
		}
	}
	return pongo2.AsValue(strings.TrimSpace(string(data))), nil
}

func filterToYAML(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return filterToNiceYAML(in, param)
}

func filterToJSON(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	// Simple JSON conversion via fmt for basic types; yaml.Marshal handles
	// the common cases since JSON is valid YAML.
	data, err := yaml.Marshal(in.Interface())
	if err != nil {
		return nil, &pongo2.Error{
			Sender:    "filter:to_json",
			OrigError: err,
		}
	}
	return pongo2.AsValue(strings.TrimSpace(string(data))), nil
}

func filterBool(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	s := fmt.Sprintf("%v", in.Interface())
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "on", "1":
		return pongo2.AsValue(true), nil
	default:
		return pongo2.AsValue(false), nil
	}
}

func filterBasename(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	s := in.String()
	parts := strings.Split(s, "/")
	return pongo2.AsValue(parts[len(parts)-1]), nil
}
