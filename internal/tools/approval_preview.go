package tools

import (
	"bytes"
	"os"
	"strings"
)

// previewMutation computes the diff an edit would produce without touching
// disk. It returns an empty string when a faithful preview cannot be built; in
// that case the approval request still shows the path and reason.
func (r *Registry) previewMutation(name string, args map[string]any) string {
	p, err := resolvePath(r.workDir, str(args["path"]))
	if err != nil {
		return ""
	}
	before := ""
	if info, err := os.Stat(p); err == nil {
		if info.IsDir() || info.Size() > maxEditBytes {
			return ""
		}
		b, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(b, 0) >= 0 {
			return ""
		}
		before = string(b)
	}

	after := before
	switch name {
	case "file_write":
		if !hasArg(args, "content") {
			return ""
		}
		after = str(args["content"])
	case "file_edit":
		oldText := str(args["old_text"])
		if oldText == "" {
			return ""
		}
		newText := str(args["new_text"])
		count := strings.Count(before, oldText)
		switch {
		case count == 0:
			return ""
		case count > 1 && !boolArg(args, "replace_all"):
			return ""
		}
		if boolArg(args, "replace_all") {
			after = strings.ReplaceAll(before, oldText, newText)
		} else {
			after = strings.Replace(before, oldText, newText, 1)
		}
	case "multi_edit":
		edits, err := parseEditSpecs(args["edits"])
		if err != nil {
			return ""
		}
		content := before
		for _, e := range edits {
			count := strings.Count(content, e.oldText)
			if count == 0 || (count > 1 && !e.replaceAll) {
				return ""
			}
			if e.replaceAll {
				content = strings.ReplaceAll(content, e.oldText, e.newText)
			} else {
				content = strings.Replace(content, e.oldText, e.newText, 1)
			}
		}
		after = content
	default:
		return ""
	}
	return unifiedDiff(before, after, relPath(r.workDir, p))
}
