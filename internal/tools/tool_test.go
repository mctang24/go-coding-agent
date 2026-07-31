package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFileToolDefinitions(t *testing.T) {
	registered := NewWorkspaceTools().Definitions()
	if len(registered) != 7 {
		t.Fatalf("Tools() length = %d, want 7", len(registered))
	}
	wantNames := map[string]bool{
		"list_files": false, "read_file": false, "search_text": false,
		"edit_file": false, "write_file": false, "run_command": false, "verify_command": false,
	}
	for _, tool := range registered {
		if _, ok := wantNames[tool.Name]; !ok {
			t.Fatalf("Tools() contains unexpected tool %q", tool.Name)
		}
		if wantNames[tool.Name] {
			t.Fatalf("Tools() contains duplicate tool %q", tool.Name)
		}
		wantNames[tool.Name] = true
		if tool.Description == "" || tool.Parameters.Type == "" || tool.Execute == nil {
			t.Fatalf("tool %q has incomplete definition", tool.Name)
		}
		if tool.Parameters.Type != "object" || tool.Parameters.AdditionalProperties == nil || *tool.Parameters.AdditionalProperties {
			t.Fatalf("tool %q parameters = %#v", tool.Name, tool.Parameters)
		}
		if tool.Name != "run_command" && tool.Name != "verify_command" {
			description := tool.Parameters.Properties["path"].Description
			if !strings.Contains(description, `Use "." for the root`) || !strings.Contains(description, "Do not use absolute paths") {
				t.Fatalf("tool %q path description does not require relative paths", tool.Name)
			}
		}
	}
}

func TestSchemaJSON(t *testing.T) {
	schema := ObjectSchema(map[string]Schema{
		"args": ArraySchema(StringSchema(""), "arguments"),
	}, "args")
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"type":"object"`,
		`"items":{"type":"string"}`,
		`"required":["args"]`,
		`"additionalProperties":false`,
	} {
		if !strings.Contains(string(encoded), expected) {
			t.Errorf("schema JSON = %s, want %s", encoded, expected)
		}
	}
}
