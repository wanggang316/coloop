package model

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// EnvValue is either a literal string or a reference to a key in Secrets.File.
// Resolved credentials never replace this value in the task definition.
type EnvValue struct {
	Value  string
	Secret string
}

type Env map[string]EnvValue

type SecretSource struct {
	File string `yaml:"file" json:"file"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (v EnvValue) Validate() error {
	if v.Secret != "" && (!envName.MatchString(v.Secret) || v.Value != "") {
		return fmt.Errorf("env reference must contain only a valid secret key")
	}
	if strings.ContainsRune(v.Value, 0) {
		return fmt.Errorf("env value must not contain NUL")
	}
	return nil
}

func (v *EnvValue) decode(raw any) error {
	*v = EnvValue{}
	switch x := raw.(type) {
	case string:
		v.Value = x
	case map[string]any:
		key, ok := x["secret"].(string)
		if !ok || len(x) != 1 || key == "" {
			return fmt.Errorf("env value must be a string or {secret: KEY}")
		}
		v.Secret = key
	default:
		return fmt.Errorf("env value must be a string or {secret: KEY}")
	}
	return v.Validate()
}

func (v *EnvValue) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("invalid env value")
	}
	return v.decode(raw)
}

func (v EnvValue) MarshalJSON() ([]byte, error) {
	if v.Secret != "" {
		return json.Marshal(map[string]string{"secret": v.Secret})
	}
	return json.Marshal(v.Value)
}

func (v *EnvValue) UnmarshalYAML(unmarshal func(any) error) error {
	var raw any
	if err := unmarshal(&raw); err != nil {
		return fmt.Errorf("invalid env value")
	}
	return v.decode(raw)
}

func (v EnvValue) MarshalYAML() (any, error) {
	if v.Secret != "" {
		return map[string]string{"secret": v.Secret}, nil
	}
	return v.Value, nil
}

// Literals exposes only ordinary values to command templates. Secrets belong
// in the child environment, never in shell command arguments or prompt text.
func (e Env) Literals() map[string]string {
	out := make(map[string]string)
	for k, v := range e {
		if v.Secret == "" {
			out[k] = v.Value
		}
	}
	return out
}
