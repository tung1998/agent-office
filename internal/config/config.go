// Package config loads office.config.json, validates it against the embedded
// JSON Schema, and exposes the parts the server needs.
//
// Only sections used by the current milestone are typed; the rest stays in
// Raw until its package lands.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/tung1998/agent-office/schema"
)

// Config is the typed subset of office.config.json.
type Config struct {
	Version int     `json:"version"`
	Project Project `json:"project"`
	Storage Storage `json:"storage"`
	Server  Server  `json:"server"`

	// Path is the file this config was loaded from ("" when defaults).
	Path string `json:"-"`
	// Raw is the full document for sections not yet typed.
	Raw json.RawMessage `json:"-"`
}

type Project struct {
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
	Language string `json:"language"`
}

type Storage struct {
	Driver string `json:"driver"`
	Path   string `json:"path"`
	DSNEnv string `json:"dsn_env"`
}

type Server struct {
	APIAddr     string   `json:"api_addr"`
	UITokenEnv  string   `json:"ui_token_env"`
	Workers     int      `json:"workers"`
	CORSOrigins []string `json:"cors_origins"`
}

// ErrNotFound means the config file does not exist.
var ErrNotFound = errors.New("config: file not found")

// Defaults returns the config used when no file exists yet (fresh checkout,
// before `office init`): local SQLite and the API on loopback.
func Defaults() Config {
	return Config{
		Version: 1,
		Storage: Storage{Driver: "sqlite", Path: ".office/office.db"},
		Server:  Server{APIAddr: "127.0.0.1:8787", Workers: 2},
	}
}

// Load reads and validates path. Relative storage paths resolve against the
// config file's directory.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrNotFound
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := Validate(raw); err != nil {
		return Config{}, err
	}
	cfg := Defaults()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: decode: %w", err)
	}
	cfg.Path, cfg.Raw = path, raw
	if cfg.Storage.Path == "" {
		cfg.Storage.Path = Defaults().Storage.Path
	}
	if cfg.Server.APIAddr == "" {
		cfg.Server.APIAddr = Defaults().Server.APIAddr
	}
	if !filepath.IsAbs(cfg.Storage.Path) {
		cfg.Storage.Path = filepath.Join(filepath.Dir(path), cfg.Storage.Path)
	}
	return cfg, nil
}

// ValidationError lists every schema violation.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "config: invalid office.config.json:\n  - " + strings.Join(e.Problems, "\n  - ")
}

var (
	compiled *jsonschema.Schema
	printer  = message.NewPrinter(language.English)
)

func compiledSchema() (*jsonschema.Schema, error) {
	if compiled != nil {
		return compiled, nil
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema.OfficeConfig))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	const id = "https://agent-office.dev/schema/office.config.schema.json"
	if err := c.AddResource(id, doc); err != nil {
		return nil, err
	}
	s, err := c.Compile(id)
	if err != nil {
		return nil, err
	}
	compiled = s
	return s, nil
}

// Validate checks raw JSON against the embedded schema.
func Validate(raw []byte) error {
	s, err := compiledSchema()
	if err != nil {
		return fmt.Errorf("config: compile schema: %w", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return &ValidationError{Problems: []string{"not valid JSON: " + err.Error()}}
	}
	err = s.Validate(doc)
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return err
	}
	var problems []string
	collect(ve, &problems)
	if len(problems) == 0 {
		problems = []string{ve.Error()}
	}
	return &ValidationError{Problems: problems}
}

func collect(ve *jsonschema.ValidationError, out *[]string) {
	if len(ve.Causes) == 0 {
		loc := "/" + strings.Join(ve.InstanceLocation, "/")
		*out = append(*out, fmt.Sprintf("%s: %s", loc, ve.ErrorKind.LocalizedString(printer)))
		return
	}
	for _, c := range ve.Causes {
		collect(c, out)
	}
}
