package main

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// EnvSource is a project-local .env* file addressable by its filename without
// the leading dot: .env => env, .env-dev => env-dev.
type EnvSource struct {
	Name   string
	Alias  string
	Values map[string]string
}

type envResolution struct {
	Raw           string
	Value         string
	Alias         string
	Key           string
	SourceName    string
	IsRef         bool
	Resolved      bool
	MissingSource bool
	MissingKey    bool
}

func discoverEnvSources() []EnvSource {
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), ".env") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	out := make([]EnvSource, 0, len(names))
	for _, name := range names {
		vals := parseEnvFile(name)
		out = append(out, EnvSource{
			Name:   name,
			Alias:  strings.TrimPrefix(name, "."),
			Values: vals,
		})
	}
	return out
}

func parseEnvFile(path string) map[string]string {
	vals := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return vals
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		vals[key] = cleanEnvValue(val)
	}
	return vals
}

func cleanEnvValue(val string) string {
	val = strings.TrimSpace(val)
	if len(val) >= 2 {
		quote := val[0]
		if (quote == '\'' || quote == '"') && val[len(val)-1] == quote {
			if unquoted, err := strconv.Unquote(val); err == nil {
				return unquoted
			}
			return val[1 : len(val)-1]
		}
	}
	return val
}

func resolveEnvValue(raw string, sources []EnvSource) envResolution {
	r := envResolution{Raw: raw, Value: raw}
	alias, key, ok := splitEnvRef(raw)
	if !ok {
		return r
	}
	r.IsRef = true
	r.Alias = alias
	r.Key = key

	for _, src := range sources {
		if src.Alias != alias {
			continue
		}
		r.SourceName = src.Name
		if val, ok := src.Values[key]; ok {
			r.Value = val
			r.Resolved = true
			return r
		}
		r.MissingKey = true
		break
	}

	// The canonical env:KEY form also supports the process environment, preserving
	// the legacy password_env behavior and allowing use without a .env file.
	if alias == "env" {
		if val, ok := os.LookupEnv(key); ok {
			r.Value = val
			r.SourceName = "process env"
			r.Resolved = true
			r.MissingKey = false
			return r
		}
	}

	if r.SourceName == "" {
		r.MissingSource = true
	}
	return r
}

func splitEnvRef(raw string) (string, string, bool) {
	alias, key, ok := strings.Cut(strings.TrimSpace(raw), ":")
	if !ok || alias == "" || key == "" {
		return "", "", false
	}
	if !looksLikeEnvAlias(alias) {
		return "", "", false
	}
	return alias, key, true
}

func looksLikeEnvAlias(alias string) bool {
	return alias == "env" || strings.HasPrefix(alias, "env-") || strings.HasPrefix(alias, "env.")
}

func envSourceNames(sources []EnvSource) []string {
	names := make([]string, 0, len(sources))
	for _, src := range sources {
		names = append(names, src.Name)
	}
	return names
}

func absFromWorkingDir(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	baseDir, err := filepath.Abs(".")
	if err != nil {
		return path
	}
	return filepath.Join(baseDir, path)
}
