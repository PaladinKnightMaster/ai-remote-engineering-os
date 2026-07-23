package ctxindex

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// headingRe matches a Markdown ATX heading line ("#" .. "######" + a space + title).
var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)

// markdownExts is the set of extensions chunked by heading section (K2) — the
// docs/runbooks/tickets corpus (tickets under docs/tickets/ ARE .md files, so no
// separate path-based special-case is needed). Everything else — code, config,
// anything unrecognized — falls to the fixed line-window strategy below.
var markdownExts = map[string]bool{".md": true, ".markdown": true, ".txt": true}

// lineWindow is the fixed, deterministic, non-overlapping window size for the
// non-Markdown chunking strategy (K2): code/config/unknown files are sliced into
// ~80-line regions with no overlap.
const lineWindow = 80

// HashContent is the stable content hash every chunk of a file carries (ADR-0066 §2).
// T-097's incremental indexer computes this BEFORE deciding whether to re-chunk a
// candidate file (K6) — sha256 hex, stdlib only, no new dependency (R3).
func HashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// ChunkFile deterministically splits one file's content into indexed chunks (ADR-0066
// §2, K2). It is a PURE function: the same (path, content) always yields the same
// chunks, in the same order, with the same locators and the same FileHash — no
// tree-sitter, no AST, no external dependency (R3; deferred per ADR-0066 §Deferred).
//
// Markdown/text files (.md, .markdown, .txt — tickets included, they are .md) are
// chunked by HEADING SECTION: each heading gets its own chunk running from that
// heading's line through the line before the next heading at the same or a shallower
// depth (so a heading's chunk includes its own nested subsections' text too — every
// heading, at every depth, gets its own separately-locatable chunk). The locator is a
// slash-joined slug path of the heading's ancestors, e.g. "heading:setup/prerequisites".
// A file with no headings yields exactly one chunk, locator "whole".
//
// Every other file (code/config/unknown) is chunked by a fixed, non-overlapping
// LINE WINDOW (lineWindow lines per chunk); the locator is "lines:<start>-<end>"
// (1-based, inclusive).
//
// An empty file yields zero chunks.
func ChunkFile(path string, content []byte) []Chunk {
	if len(content) == 0 {
		return nil
	}
	hash := HashContent(content)
	var chunks []Chunk
	if markdownExts[strings.ToLower(fileExt(path))] {
		chunks = chunkByHeading(content)
	} else {
		chunks = chunkByLineWindow(content)
	}
	for i := range chunks {
		chunks[i].FilePath = path
		chunks[i].FileHash = hash
	}
	return chunks
}

func fileExt(path string) string {
	i := strings.LastIndexByte(path, '.')
	if i < 0 {
		return ""
	}
	return path[i:]
}

// splitLines splits content into lines, stdlib-only and deterministic: a single
// trailing newline never produces a spurious empty final line.
func splitLines(content []byte) []string {
	s := strings.TrimRight(string(content), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

type heading struct {
	lineIdx int // 0-based index into lines
	depth   int
	title   string
}

func chunkByHeading(content []byte) []Chunk {
	lines := splitLines(content)
	var heads []heading
	for i, l := range lines {
		m := headingRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		heads = append(heads, heading{lineIdx: i, depth: len(m[1]), title: strings.TrimSpace(m[2])})
	}
	if len(heads) == 0 {
		return []Chunk{{Locator: "whole", Content: strings.Join(lines, "\n")}}
	}

	chunks := make([]Chunk, 0, len(heads))
	var stackSlugs []string
	var stackDepths []int
	for i, h := range heads {
		for len(stackDepths) > 0 && stackDepths[len(stackDepths)-1] >= h.depth {
			stackSlugs = stackSlugs[:len(stackSlugs)-1]
			stackDepths = stackDepths[:len(stackDepths)-1]
		}
		stackSlugs = append(stackSlugs, slugify(h.title))
		stackDepths = append(stackDepths, h.depth)
		locator := "heading:" + strings.Join(stackSlugs, "/")

		end := len(lines)
		for _, nh := range heads[i+1:] {
			if nh.depth <= h.depth {
				end = nh.lineIdx
				break
			}
		}
		chunks = append(chunks, Chunk{Locator: locator, Content: strings.Join(lines[h.lineIdx:end], "\n")})
	}
	return chunks
}

func chunkByLineWindow(content []byte) []Chunk {
	lines := splitLines(content)
	if len(lines) == 0 {
		return nil // whitespace-only content — treat as no indexable lines
	}
	chunks := make([]Chunk, 0, (len(lines)/lineWindow)+1)
	for start := 0; start < len(lines); start += lineWindow {
		end := start + lineWindow
		if end > len(lines) {
			end = len(lines)
		}
		chunks = append(chunks, Chunk{
			Locator: fmt.Sprintf("lines:%d-%d", start+1, end),
			Content: strings.Join(lines[start:end], "\n"),
		})
	}
	return chunks
}

// slugify lower-cases s and collapses every run of non [a-z0-9] characters into a
// single '-', trimming any leading/trailing dash — a stable, dependency-free anchor
// suitable for a locator path segment.
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := true // suppresses a leading dash
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}
