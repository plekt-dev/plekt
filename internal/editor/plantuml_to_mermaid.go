package editor

import (
	"regexp"
	"strings"
)

// PlantUMLToMermaid converts basic PlantUML syntax to Mermaid syntax.
// Supports sequence diagrams and simple component diagrams.
// Returns empty string if the diagram type is not supported.
func PlantUMLToMermaid(src string) string {
	src = strings.TrimSpace(src)
	src = stripPlantUMLWrapper(src)
	if src == "" {
		return ""
	}

	lines := strings.Split(src, "\n")

	// Detect diagram type from content.
	if isSequenceDiagram(lines) {
		return convertSequenceDiagram(lines)
	}

	// Default: try as a generic component/flow diagram.
	return convertFlowDiagram(lines)
}

func stripPlantUMLWrapper(src string) string {
	src = strings.TrimPrefix(src, "@startuml")
	src = strings.TrimSuffix(src, "@enduml")
	return strings.TrimSpace(src)
}

// isSequenceDiagram detects sequence diagram patterns: arrows like ->, -->, ->>, etc.
func isSequenceDiagram(lines []string) bool {
	arrowCount := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "'") || strings.HasPrefix(line, "title") {
			continue
		}
		if seqArrowRe.MatchString(line) {
			arrowCount++
		}
	}
	return arrowCount > 0
}

var seqArrowRe = regexp.MustCompile(`\S+\s+(->>?|-->>?|->|-->)\s+\S+`)
var seqLineRe = regexp.MustCompile(`^(\S+)\s+(->>?|-->>?|->|-->)\s+(\S+)\s*:\s*(.*)$`)
var actorRe = regexp.MustCompile(`^(?:actor|participant|entity|database|boundary|control|collections)\s+(.+)$`)
var noteRe = regexp.MustCompile(`^note\s+`)

func convertSequenceDiagram(lines []string) string {
	var out []string
	out = append(out, "sequenceDiagram")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "'") {
			continue
		}

		// Title
		if strings.HasPrefix(line, "title ") {
			// Mermaid doesn't have title in sequenceDiagram, skip.
			continue
		}

		// Actor/participant declarations
		if m := actorRe.FindStringSubmatch(line); m != nil {
			name := cleanName(m[1])
			out = append(out, "    participant "+name)
			continue
		}

		// Skip notes (not directly supported in basic mermaid sequence)
		if noteRe.MatchString(line) {
			continue
		}

		// Arrow lines: A -> B: message
		if m := seqLineRe.FindStringSubmatch(line); m != nil {
			from := cleanName(m[1])
			arrow := convertSeqArrow(m[2])
			to := cleanName(m[3])
			msg := strings.TrimSpace(m[4])
			if msg == "" {
				msg = " "
			}
			out = append(out, "    "+from+arrow+to+": "+msg)
			continue
		}

		// Skip unsupported lines.
	}

	if len(out) <= 1 {
		return ""
	}
	return strings.Join(out, "\n")
}

func convertSeqArrow(pumlArrow string) string {
	switch pumlArrow {
	case "->":
		return "->>"
	case "-->":
		return "-->>"
	case "->>":
		return "->>"
	case "-->>":
		return "-->>"
	default:
		return "->>"
	}
}

var flowArrowRe = regexp.MustCompile(`^(\S+)\s+([-=][-=]>|->|-->)\s+(\S+)\s*(?::\s*(.*))?$`)
var componentRe = regexp.MustCompile(`^(?:component|node|rectangle|package|cloud|folder|frame|storage|queue|stack|card)\s+(?:\[(.+?)\]|"(.+?)"|(\S+))(?:\s+as\s+(\S+))?`)

func convertFlowDiagram(lines []string) string {
	var out []string
	out = append(out, "graph LR")

	aliases := make(map[string]string)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "'") {
			continue
		}

		// Component declarations: component [Name] as alias
		if m := componentRe.FindStringSubmatch(line); m != nil {
			name := firstNonEmpty(m[1], m[2], m[3])
			alias := m[4]
			if alias != "" {
				aliases[alias] = name
				out = append(out, "    "+alias+"["+name+"]")
			}
			continue
		}

		// Arrow lines: A --> B : label
		if m := flowArrowRe.FindStringSubmatch(line); m != nil {
			from := cleanName(m[1])
			to := cleanName(m[3])
			label := strings.TrimSpace(m[4])
			if label != "" {
				out = append(out, "    "+from+" -->|"+label+"| "+to)
			} else {
				out = append(out, "    "+from+" --> "+to)
			}
			continue
		}

		// Simple arrow without regex: try to handle "A -> B" patterns
		if strings.Contains(line, "->") || strings.Contains(line, "-->") {
			parts := strings.SplitN(line, ":", 2)
			arrowPart := strings.TrimSpace(parts[0])
			label := ""
			if len(parts) > 1 {
				label = strings.TrimSpace(parts[1])
			}

			// Split on arrow
			var from, to string
			if strings.Contains(arrowPart, "-->") {
				segs := strings.SplitN(arrowPart, "-->", 2)
				from = cleanName(strings.TrimSpace(segs[0]))
				to = cleanName(strings.TrimSpace(segs[1]))
			} else if strings.Contains(arrowPart, "->") {
				segs := strings.SplitN(arrowPart, "->", 2)
				from = cleanName(strings.TrimSpace(segs[0]))
				to = cleanName(strings.TrimSpace(segs[1]))
			}
			if from != "" && to != "" {
				if label != "" {
					out = append(out, "    "+from+" -->|"+label+"| "+to)
				} else {
					out = append(out, "    "+from+" --> "+to)
				}
			}
			continue
		}
	}

	if len(out) <= 1 {
		return ""
	}
	return strings.Join(out, "\n")
}

func cleanName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"[]`)
	// Replace spaces with underscores for mermaid compatibility.
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
