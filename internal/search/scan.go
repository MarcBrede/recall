package search

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	NodeTypeSession = "session"
	NodeTypeSegment = "segment"
	NodeTypeSection = "section"
)

type nodeInput struct {
	NodeType    string
	MemoryPath  string
	Content     string
	ContentHash string
	LastEventAt string
}

func scanMemoryNodes(recallDir string, scopeDir string) ([]nodeInput, error) {
	if strings.TrimSpace(recallDir) == "" {
		return nil, fmt.Errorf("search: recall dir is required")
	}
	sessionsDir := filepath.Join(recallDir, "sessions")
	if strings.TrimSpace(scopeDir) == "" {
		scopeDir = sessionsDir
	}
	scopeDir = filepath.Clean(scopeDir)
	if !pathWithin(sessionsDir, scopeDir) {
		return nil, fmt.Errorf("search: scope must be under %s", sessionsDir)
	}

	var nodes []nodeInput
	err := filepath.WalkDir(scopeDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") && path != scopeDir {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeType != 0 || filepath.Ext(path) != ".md" {
			return nil
		}

		nodeType := nodeTypeForPath(path)
		if nodeType == "" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		frontmatter, body := splitFrontmatter(string(data))
		summary := frontmatterBlock(frontmatter, "summary")
		content := strings.TrimSpace(strings.Join([]string{summary, strings.TrimSpace(body)}, "\n\n"))
		if content == "" {
			return nil
		}
		rel, err := filepath.Rel(recallDir, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(content))
		nodes = append(nodes, nodeInput{
			NodeType:    nodeType,
			MemoryPath:  filepath.ToSlash(rel),
			Content:     content,
			ContentHash: fmt.Sprintf("sha256:%x", sum),
			LastEventAt: frontmatterValue(frontmatter, "last_event_at"),
		})
		return nil
	})
	return nodes, err
}

func nodeTypeForPath(path string) string {
	name := filepath.Base(path)
	switch name {
	case "session.md":
		return NodeTypeSession
	case "segment.md":
		return NodeTypeSegment
	}
	if filepath.Base(filepath.Dir(path)) == "sections" && strings.HasSuffix(name, ".md") {
		return NodeTypeSection
	}
	return ""
}

func splitFrontmatter(content string) ([]string, string) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, content
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return lines[1:i], strings.Join(lines[i+1:], "\n")
		}
	}
	return nil, content
}

func frontmatterValue(lines []string, key string) string {
	prefix := key + ":"
	for _, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
		return value
	}
	return ""
}

func frontmatterBlock(lines []string, key string) string {
	prefix := key + ":"
	for i, line := range lines {
		if strings.TrimSpace(line) != prefix+" |" {
			continue
		}
		var block []string
		for _, valueLine := range lines[i+1:] {
			if strings.HasPrefix(valueLine, "  ") {
				block = append(block, strings.TrimPrefix(valueLine, "  "))
				continue
			}
			if strings.TrimSpace(valueLine) == "" {
				block = append(block, "")
				continue
			}
			break
		}
		return strings.TrimSpace(strings.Join(block, "\n"))
	}
	return ""
}

func pathWithin(parent string, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
