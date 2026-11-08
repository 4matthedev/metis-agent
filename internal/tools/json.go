package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// NewJSONQueryTool reads and queries JSON documents.
func NewJSONQueryTool(workDir string) *Tool {
	return &Tool{
		Name: "json_query",
		Description: "Parse a JSON file or inline JSON and extract values with a dot/bracket path " +
			"(for example .dependencies.react or .items[0].name). With no query, the whole document is pretty-printed.",
		Parameters: objectSchema(map[string]any{
			"path":  propSchema{Type: "string", Description: "JSON file relative to the project, or absolute."},
			"json":  propSchema{Type: "string", Description: "Inline JSON document (alternative to path)."},
			"query": propSchema{Type: "string", Description: "Path such as .foo.bar or .items[0].name."},
			"queries": map[string]any{
				"type":        "array",
				"description": "Several query paths.",
				"items":       map[string]any{"type": "string"},
			},
			"indent": propSchema{Type: "integer", Description: "Indent width (default 2, 0 for compact)."},
		}),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			raw := str(args["json"])
			pathArg := strings.TrimSpace(str(args["path"]))
			if raw == "" && pathArg == "" {
				return "", fmt.Errorf("path or json is required")
			}
			if raw != "" && pathArg != "" {
				return "", fmt.Errorf("provide either path or json, not both")
			}
			if pathArg != "" {
				p, err := resolvePath(workDir, pathArg)
				if err != nil {
					return "", err
				}
				info, err := os.Stat(p)
				if err != nil {
					return "", fmt.Errorf("read %s: %w", relPath(workDir, p), err)
				}
				if info.IsDir() {
					return "", fmt.Errorf("%s is a directory", relPath(workDir, p))
				}
				if info.Size() > 16*1024*1024 {
					return "", fmt.Errorf("%s is too large for json_query (%s)", relPath(workDir, p), humanSize(info.Size()))
				}
				b, err := os.ReadFile(p)
				if err != nil {
					return "", fmt.Errorf("read %s: %w", relPath(workDir, p), err)
				}
				if bytes.IndexByte(b, 0) >= 0 {
					return "", fmt.Errorf("%s looks like a binary file", relPath(workDir, p))
				}
				raw = string(b)
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}

			var doc any
			dec := json.NewDecoder(strings.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&doc); err != nil {
				return "", fmt.Errorf("invalid JSON: %w", err)
			}
			var extra any
			if err := dec.Decode(&extra); err != io.EOF {
				return "", fmt.Errorf("invalid JSON: trailing data after the first value")
			}

			queries := stringList(args["queries"])
			if q := strings.TrimSpace(str(args["query"])); q != "" {
				queries = append([]string{q}, queries...)
			}
			indent := clamp(intArg(args, "indent", 2), 0, 8)
			if len(queries) == 0 {
				return formatJSON(doc, indent), nil
			}

			var b strings.Builder
			for i, q := range queries {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				if i > 0 {
					b.WriteByte('\n')
				}
				value, err := jsonLookup(doc, q)
				if err != nil {
					fmt.Fprintf(&b, "%s: error: %v\n", q, err)
					continue
				}
				fmt.Fprintf(&b, "%s:\n%s\n", q, formatJSON(value, indent))
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}

func formatJSON(v any, indent int) string {
	var b []byte
	var err error
	if indent > 0 {
		b, err = json.MarshalIndent(v, "", strings.Repeat(" ", indent))
	} else {
		b, err = json.Marshal(v)
	}
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return string(b)
}

// jsonLookup evaluates a small JSON path language: keys separated by dots and
// integer indexes in brackets. A leading $ or dot is optional.
func jsonLookup(v any, query string) (any, error) {
	q := strings.TrimSpace(query)
	q = strings.TrimPrefix(q, "$")
	q = strings.TrimPrefix(q, ".")
	cur := v
	for q != "" {
		switch q[0] {
		case '[':
			end := strings.IndexByte(q, ']')
			if end < 0 {
				return nil, fmt.Errorf("unclosed '[' in %q", query)
			}
			idx, err := strconv.Atoi(strings.TrimSpace(q[1:end]))
			if err != nil {
				return nil, fmt.Errorf("invalid index %q", q[1:end])
			}
			arr, ok := cur.([]any)
			if !ok {
				return nil, fmt.Errorf("cannot index a %s value with [%d]", jsonType(cur), idx)
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("index %d out of range (length %d)", idx, len(arr))
			}
			cur = arr[idx]
			q = strings.TrimPrefix(q[end+1:], ".")
			q = strings.TrimPrefix(q, ".")
		default:
			i := strings.IndexAny(q, ".[")
			key := q
			if i >= 0 {
				key = q[:i]
			}
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("cannot read key %q from a %s value", key, jsonType(cur))
			}
			next, ok := m[key]
			if !ok {
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				return nil, fmt.Errorf("key %q not found (available: %s)", key, strings.Join(keys, ", "))
			}
			cur = next
			if i < 0 {
				q = ""
			} else {
				q = strings.TrimPrefix(q[i:], ".")
				q = strings.TrimPrefix(q, ".")
			}
		}
	}
	return cur, nil
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number, float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}
