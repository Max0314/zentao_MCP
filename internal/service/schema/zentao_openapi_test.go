package schema

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

type staticLoader struct {
	doc *openapi3.T
}

func (l staticLoader) Load(context.Context) (*openapi3.T, error) {
	return l.doc, nil
}

func TestZentaoOpenAPIDocumentIsCalibratedFor12Point3V1(t *testing.T) {
	ctx := context.Background()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "..", "docs", "zentao-openapi.json"))
	if err != nil {
		t.Fatalf("load zentao openapi: %v", err)
	}
	if err := doc.Validate(ctx); err != nil {
		t.Fatalf("validate zentao openapi: %v", err)
	}

	if got := doc.Info.Version; got != "12.3-v1" {
		t.Fatalf("info.version = %q, want 12.3-v1", got)
	}
	if doc.Paths.Find("/tokens") == nil || doc.Paths.Find("/tokens").Post == nil {
		t.Fatal("POST /tokens is missing")
	}
	if doc.Paths.Find("/executions/{executionID}/tasks") == nil || doc.Paths.Find("/executions/{executionID}/tasks").Post == nil {
		t.Fatal("POST /executions/{executionID}/tasks is missing")
	}
	if postTasks := doc.Paths.Find("/tasks").Post; postTasks == nil || !postTasks.Deprecated {
		t.Fatal("POST /tasks should remain documented but deprecated for 12.3")
	}
	if usersLogin := doc.Paths.Find("/users/login").Post; usersLogin == nil || !usersLogin.Deprecated {
		t.Fatal("POST /users/login should remain documented but deprecated for 12.3")
	}
	bugCreate := doc.Paths.Find("/bugs").Post
	if bugCreate == nil {
		t.Fatal("POST /bugs is missing")
	}
	bugPayload := bugCreate.RequestBody.Value.Content["application/json"].Schema.Value
	if _, ok := bugPayload.Properties["product"]; !ok {
		t.Fatal("POST /bugs payload should use product for Zentao 12.3 v1")
	}
	if _, ok := bugPayload.Properties["productID"]; ok {
		t.Fatal("POST /bugs payload should not require productID for Zentao 12.3 v1")
	}
	if activateBug := doc.Paths.Find("/bugs/{bugID}/activate").Put; activateBug == nil || !activateBug.Deprecated {
		t.Fatal("PUT /bugs/{bugID}/activate should be marked deprecated for 12.3")
	}
	if activateTask := doc.Paths.Find("/tasks/{taskID}/activate").Put; activateTask == nil || !activateTask.Deprecated {
		t.Fatal("PUT /tasks/{taskID}/activate should be marked deprecated for 12.3")
	}

	tools, err := New(staticLoader{doc: doc}).Tools(ctx)
	if err != nil {
		t.Fatalf("build tools: %v", err)
	}

	var hasCreateExecutionTask bool
	for _, tool := range tools {
		if tool.OperationID == "post_executions_executionID_tasks" {
			hasCreateExecutionTask = true
			if tool.Path != "/executions/{executionID}/tasks" {
				t.Fatalf("create task path = %q, want /executions/{executionID}/tasks", tool.Path)
			}
		}
	}
	if !hasCreateExecutionTask {
		t.Fatal("generated tools are missing post_executions_executionID_tasks")
	}
}
