package apiref_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/FacileStudio/tronc/apiref"
)

func registry() apiref.Registry {
	return apiref.Registry{Modules: []apiref.Module{{
		Name:        "projects",
		Description: "Shared projects",
		Routes: []apiref.Route{
			{
				Method:       "GET",
				Path:         "/projects",
				Summary:      "List projects",
				Auth:         "bearer",
				ResponseBody: "[]Project",
			},
			{
				Method:      "POST",
				Path:        "/projects/{id}",
				Summary:     "Update a project",
				Auth:        "bearer",
				PathParams:  []apiref.Field{{Name: "id", Type: "int", Description: "Project ID"}},
				RequestBody: "UpdateProjectInput",
				Errors:      []apiref.Error{{Status: 404, Code: "not_found", Description: "No such project"}},
			},
		},
	}}}
}

func config() apiref.Config {
	return apiref.Config{
		Title:       "Sablier API",
		Description: "Time tracking",
		Servers:     []string{"/api"},
		Registry:    registry(),
	}
}

func TestMountServesReferenceAndSpec(t *testing.T) {
	router := chi.NewRouter()
	apiref.Mount(router, config())

	page := httptest.NewRecorder()
	router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("GET /docs = %d, want 200", page.Code)
	}
	body := page.Body.String()
	if !strings.Contains(body, `data-url="/docs/openapi.json"`) {
		t.Errorf("reference page does not point at its own spec:\n%s", body)
	}
	if !strings.Contains(body, apiref.ScalarScriptURL) {
		t.Errorf("reference page does not load the pinned Scalar bundle")
	}

	spec := httptest.NewRecorder()
	router.ServeHTTP(spec, httptest.NewRequest(http.MethodGet, "/docs/openapi.json", nil))
	if spec.Code != http.StatusOK {
		t.Fatalf("GET /docs/openapi.json = %d, want 200", spec.Code)
	}
	var document map[string]any
	if err := json.Unmarshal(spec.Body.Bytes(), &document); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if document["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v, want 3.1.0", document["openapi"])
	}
}

func TestSpecURLFollowsTheMountPoint(t *testing.T) {
	router := chi.NewRouter()
	router.Route("/api", func(r chi.Router) { apiref.Mount(r, config()) })

	page := httptest.NewRecorder()
	router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	if !strings.Contains(page.Body.String(), `data-url="/api/docs/openapi.json"`) {
		t.Errorf("spec URL did not follow the mount point:\n%s", page.Body.String())
	}
}

func TestOpenAPIDeclaresTheSecuritySchemeItUses(t *testing.T) {
	document := apiref.OpenAPI(config())

	components, ok := document["components"].(map[string]any)
	if !ok {
		t.Fatal("document has no components")
	}
	schemes, ok := components["securitySchemes"].(map[string]any)
	if !ok || schemes["bearerAuth"] == nil {
		t.Fatal("bearerAuth is referenced by operations but never declared")
	}
}

func TestOpenAPIEmitsParametersAndBodies(t *testing.T) {
	document := apiref.OpenAPI(config())
	paths := document["paths"].(map[string]any)

	update := paths["/projects/{id}"].(map[string]any)["post"].(map[string]any)
	params, ok := update["parameters"].([]any)
	if !ok || len(params) != 1 {
		t.Fatalf("path parameters = %v, want one", update["parameters"])
	}
	if name := params[0].(map[string]any)["name"]; name != "id" {
		t.Errorf("parameter name = %v, want id", name)
	}
	if update["requestBody"] == nil {
		t.Error("documented request body was dropped from the operation")
	}
	if _, found := update["responses"].(map[string]any)["404"]; !found {
		t.Error("documented error status was dropped from the operation")
	}
}

func TestOpenAPIDefaultsVersionAndOmitsEmptyServers(t *testing.T) {
	cfg := config()
	cfg.Servers = nil
	document := apiref.OpenAPI(cfg)

	if version := document["info"].(map[string]any)["version"]; version != apiref.DefaultVersion {
		t.Errorf("version = %v, want %s", version, apiref.DefaultVersion)
	}
	if _, present := document["servers"]; present {
		t.Error("servers should be omitted when none are configured")
	}
}

func TestUndocumentedFindsRoutesMissingFromTheRegistry(t *testing.T) {
	router := chi.NewRouter()
	apiref.Mount(router, config())
	router.Get("/health", func(http.ResponseWriter, *http.Request) {})
	router.Handle("/*", http.NotFoundHandler())
	router.Route("/api", func(r chi.Router) {
		r.Get("/projects", func(http.ResponseWriter, *http.Request) {})
		r.Post("/projects/{id}", func(http.ResponseWriter, *http.Request) {})
		r.Delete("/projects/{id}", func(http.ResponseWriter, *http.Request) {})
	})

	missing := apiref.Undocumented(router, config())
	want := []string{"DELETE /api/projects/{id}"}
	if len(missing) != len(want) || missing[0] != want[0] {
		t.Errorf("Undocumented() = %v, want %v", missing, want)
	}
}

func TestUndocumentedNormalizesRegexParameters(t *testing.T) {
	router := chi.NewRouter()
	router.Route("/api", func(r chi.Router) {
		r.Get("/projects/{id:[0-9]+}", func(http.ResponseWriter, *http.Request) {})
	})

	cfg := config()
	cfg.Registry = apiref.Registry{Modules: []apiref.Module{{
		Name:   "projects",
		Routes: []apiref.Route{{Method: "GET", Path: "/projects/{id}"}},
	}}}

	if missing := apiref.Undocumented(router, cfg); len(missing) != 0 {
		t.Errorf("regex-constrained parameter was not matched to its registry entry: %v", missing)
	}
}

type CreateProjectInput struct {
	Name        string `json:"name" validate:"required"`
	Description string `json:"description,omitempty"`
	Private     bool   `json:"private"`
}

type ProjectOutput struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt string    `json:"created_at"`
}

func TestOpenAPIReflectsStructSchemasAndComponents(t *testing.T) {
	cfg := apiref.Config{
		Title: "Reflect API",
		Registry: apiref.Registry{
			Modules: []apiref.Module{{
				Name: "projects",
				Routes: []apiref.Route{
					{
						Method:       "POST",
						Path:         "/projects",
						Summary:      "Create project",
						QueryParams:  []apiref.Field{{Name: "notify", Type: "bool", Description: "Send notification"}},
						RequestBody:  CreateProjectInput{},
						ResponseBody: ProjectOutput{},
						Status:       http.StatusCreated,
					},
					{
						Method:  "DELETE",
						Path:    "/projects/{id}",
						Summary: "Delete project",
						Status:  http.StatusNoContent,
					},
				},
			}},
		},
	}

	doc := apiref.OpenAPI(cfg)
	components, ok := doc["components"].(map[string]any)
	if !ok {
		t.Fatal("components missing")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("schemas missing in components")
	}

	createSchema, ok := schemas["CreateProjectInput"].(map[string]any)
	if !ok {
		t.Fatal("CreateProjectInput schema missing")
	}
	props, ok := createSchema["properties"].(map[string]any)
	if !ok || props["name"] == nil || props["description"] == nil || props["private"] == nil {
		t.Errorf("properties in CreateProjectInput incomplete: %v", props)
	}

	paths := doc["paths"].(map[string]any)
	postOp := paths["/projects"].(map[string]any)["post"].(map[string]any)
	reqBody := postOp["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if reqBody["$ref"] != "#/components/schemas/CreateProjectInput" {
		t.Errorf("requestBody ref = %v, want #/components/schemas/CreateProjectInput", reqBody["$ref"])
	}

	resp201 := postOp["responses"].(map[string]any)["201"].(map[string]any)
	respSchema := resp201["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if respSchema["$ref"] != "#/components/schemas/ProjectOutput" {
		t.Errorf("response ref = %v, want #/components/schemas/ProjectOutput", respSchema["$ref"])
	}

	deleteOp := paths["/projects/{id}"].(map[string]any)["delete"].(map[string]any)
	resp204 := deleteOp["responses"].(map[string]any)["204"].(map[string]any)
	if resp204["content"] != nil {
		t.Errorf("204 response should not have content body: %v", resp204)
	}
}

func TestIncompleteFindsThinOrMissingRouteDeclarations(t *testing.T) {
	cfg := apiref.Config{
		Title: "Test API",
		Registry: apiref.Registry{
			Modules: []apiref.Module{{
				Name: "test",
				Routes: []apiref.Route{
					{
						Method:  "POST",
						Path:    "/empty-summary",
						Summary: "",
					},
					{
						Method:  "POST",
						Path:    "/no-body",
						Summary: "Has summary but no body",
					},
					{
						Method:       "GET",
						Path:         "/good",
						Summary:      "Good route",
						ResponseBody: "GoodResponse",
					},
				},
			}},
		},
	}

	issues := apiref.Incomplete(cfg)
	if len(issues) < 2 {
		t.Fatalf("Incomplete() = %v, want at least 2 issues", issues)
	}
}
