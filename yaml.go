package copier

import "gopkg.in/yaml.v3"

// yamlUnmarshalDirect is the single point for YAML deserialization.
func yamlUnmarshalDirect(data []byte, v any) error {
	return yaml.Unmarshal(data, v)
}
