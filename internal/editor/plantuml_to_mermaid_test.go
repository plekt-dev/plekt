package editor_test

import (
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/editor"
)

func TestPlantUMLToMermaid_SequenceDiagram(t *testing.T) {
	src := `@startuml
actor User
User -> System: Request
System --> User: Response
@enduml`

	result := editor.PlantUMLToMermaid(src)
	if result == "" {
		t.Fatal("expected non-empty mermaid output")
	}

	if !strings.Contains(result, "sequenceDiagram") {
		t.Errorf("expected sequenceDiagram header, got:\n%s", result)
	}
	if !strings.Contains(result, "User") {
		t.Errorf("expected User in output, got:\n%s", result)
	}
	if !strings.Contains(result, "System") {
		t.Errorf("expected System in output, got:\n%s", result)
	}
	if !strings.Contains(result, "Request") {
		t.Errorf("expected Request label, got:\n%s", result)
	}
	if !strings.Contains(result, "Response") {
		t.Errorf("expected Response label, got:\n%s", result)
	}
	t.Logf("Converted:\n%s", result)
}

func TestPlantUMLToMermaid_ArrowDiagramAsSequence(t *testing.T) {
	src := `@startuml
A -> B: process
B --> C: forward
@enduml`

	result := editor.PlantUMLToMermaid(src)
	if result == "" {
		t.Fatal("expected non-empty mermaid output")
	}
	// Arrow diagrams are detected as sequence diagrams
	if !strings.Contains(result, "sequenceDiagram") && !strings.Contains(result, "graph") {
		t.Errorf("expected diagram header, got:\n%s", result)
	}
	if !strings.Contains(result, "A") || !strings.Contains(result, "B") {
		t.Errorf("expected nodes, got:\n%s", result)
	}
	t.Logf("Converted:\n%s", result)
}

func TestPlantUMLToMermaid_Empty(t *testing.T) {
	result := editor.PlantUMLToMermaid("")
	if result != "" {
		t.Errorf("expected empty for empty input, got: %q", result)
	}
}

func TestPlantUMLToMermaid_RendersAsMermaidBlock(t *testing.T) {
	r := editor.NewRenderer(nil)
	opts := editor.DefaultRenderOptions("")

	src := "```plantuml\n@startuml\nactor User\nUser -> System: Request\nSystem --> User: Response\n@enduml\n```"
	html, err := r.Render([]byte(src), opts)
	if err != nil {
		t.Fatal(err)
	}
	output := string(html)
	t.Logf("Rendered:\n%s", output)

	// Should render as mermaid block (converted from PlantUML)
	if !strings.Contains(output, `class="mermaid"`) {
		t.Errorf("expected mermaid class (converted from PlantUML), got:\n%s", output)
	}
	if !strings.Contains(output, "sequenceDiagram") {
		t.Errorf("expected sequenceDiagram in converted output, got:\n%s", output)
	}
}
