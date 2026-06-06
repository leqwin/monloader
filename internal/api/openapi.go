package api

import (
	"encoding/json"
	"html/template"
	"maps"
	"net/http"
	"slices"
	"strings"
)

// buildSpec assembles the OpenAPI 3 document in code, mirroring monbooru's
// approach. The server is the root base URL so /health (root)
// and the /api/v1/* endpoints share one document.
func buildSpec(baseURL string) map[string]any {
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "monloader API",
			"description": "Queue booru URLs for download into monbooru.",
			"version":     "1.0.0",
		},
		"servers": []map[string]any{
			{"url": baseURL, "description": "This server"},
		},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "token",
					"description":  "Required only when auth.api_token is configured; the API is open otherwise (LAN trust).",
				},
			},
			"schemas": map[string]any{
				"Error": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"error": map[string]any{"type": "string"},
						"code":  map[string]any{"type": "string"},
					},
				},
				"Summary": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"created":   map[string]any{"type": "integer"},
						"duplicate": map[string]any{"type": "integer"},
						"skipped":   map[string]any{"type": "integer", "description": "Posts the gallery-dl archive already had"},
						"failed":    map[string]any{"type": "integer"},
						"canceled":  map[string]any{"type": "integer", "description": "Items aborted by a job cancel, kept out of failed"},
						"total":     map[string]any{"type": "integer"},
					},
				},
				"Item": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"post_id":      map[string]any{"type": "string"},
						"num":          map[string]any{"type": "integer", "description": "1-based pool page order"},
						"url":          map[string]any{"type": "string", "description": "canonical source post page"},
						"status":       map[string]any{"type": "string", "description": "pending, downloaded, uploaded, done, skipped, failed"},
						"outcome":      map[string]any{"type": "string", "description": "created, duplicate, skipped_archive, failed"},
						"monbooru_id":  map[string]any{"type": "integer"},
						"sha256":       map[string]any{"type": "string"},
						"tag_warnings": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Tags monbooru rejected on the push; recorded, not fatal"},
						"error_code":   map[string]any{"type": "string"},
						"error":        map[string]any{"type": "string"},
					},
				},
				"Job": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":          map[string]any{"type": "integer"},
						"url":         map[string]any{"type": "string"},
						"status":      map[string]any{"type": "string", "description": "queued, running, succeeded, partial, failed, canceled"},
						"site":        map[string]any{"type": "string", "description": "gallery-dl category, set after resolve"},
						"gallery":     map[string]any{"type": "string", "description": "target monbooru gallery"},
						"force":       map[string]any{"type": "boolean", "description": "Last/next run bypasses the gallery-dl archive (set by a forced retry)"},
						"summary":     map[string]any{"$ref": "#/components/schemas/Summary"},
						"capped":      map[string]any{"type": "boolean", "description": "The resolve hit the per-job item cap, so more posts may remain"},
						"cap":         map[string]any{"type": "integer", "description": "The applied item cap when capped is true"},
						"error_code":  map[string]any{"type": "string"},
						"error":       map[string]any{"type": "string"},
						"items":       map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Item"}},
						"created_at":  map[string]any{"type": "string", "format": "date-time"},
						"started_at":  map[string]any{"type": "string", "format": "date-time"},
						"finished_at": map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"PaginatedJobs": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"page":  map[string]any{"type": "integer"},
						"limit": map[string]any{"type": "integer"},
						"total": map[string]any{"type": "integer"},
						"jobs":  map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Job"}},
					},
				},
				"Site": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"category":    map[string]any{"type": "string"},
						"subcategory": map[string]any{"type": "string"},
						"example":     map[string]any{"type": "string"},
						"curated":     map[string]any{"type": "boolean", "description": "Has a built-in mapping profile"},
						"auth":        map[string]any{"type": "string", "description": "none, api_optional, api_required, cookies, oauth"},
					},
				},
				"ProbeResult": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status": map[string]any{"type": "string", "description": "ok, auth_required, blocked, failed"},
						"detail": map[string]any{"type": "string"},
					},
				},
				"Health": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":            map[string]any{"type": "string"},
						"version":           map[string]any{"type": "string"},
						"gallerydl_version": map[string]any{"type": "string"},
					},
				},
				"EnqueueResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"job_id": map[string]any{"type": "integer"},
					},
				},
			},
		},
		"security": []map[string]any{
			{"bearerAuth": []string{}},
		},
		"paths": map[string]any{
			"/health": map[string]any{
				"get": map[string]any{
					"summary":     "Liveness and versions",
					"operationId": "health",
					"security":    []map[string]any{},
					"responses": map[string]any{
						"200": resp("Service status and versions", "#/components/schemas/Health"),
					},
				},
			},
			"/api/v1/queue": map[string]any{
				"post": map[string]any{
					"summary":     "Enqueue a URL",
					"operationId": "enqueue",
					"parameters": []map[string]any{
						queryParam("wait", "Block up to N seconds (capped at 60) for the job to resolve and return it inline; otherwise 202 with a job id"),
					},
					"requestBody": jsonBodySchema(true, map[string]any{
						"type":     "object",
						"required": []string{"url"},
						"properties": map[string]any{
							"url": map[string]any{"type": "string", "description": "A booru post, pool, tag-search, or artist URL gallery-dl supports"},
							"options": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"gallery":   map[string]any{"type": "string", "description": "Target monbooru gallery; overrides the per-source default"},
									"folder":    map[string]any{"type": "string", "description": "Destination subfolder under the gallery"},
									"max_items": map[string]any{"type": "integer", "minimum": 1, "description": "Cap on posts fetched this run (--range 1-N); only lowers the cap, never above the server's max_items_per_job. A pool is exempt and always fetched whole"},
								},
							},
						},
					}),
					"responses": map[string]any{
						"200": resp("Resolved job (only when wait elapsed in time)", "#/components/schemas/Job"),
						"202": resp("Job accepted; poll GET /api/v1/queue/{id}", "#/components/schemas/EnqueueResponse"),
						"400": resp("Missing url or negative max_items", "#/components/schemas/Error"),
					},
				},
				"get": map[string]any{
					"summary":     "List jobs",
					"operationId": "listJobs",
					"parameters": []map[string]any{
						queryParam("status", "Filter by job status"),
						queryParam("page", "Page number (1-based)"),
						queryParam("limit", "Results per page (max 200)"),
					},
					"responses": map[string]any{
						"200": resp("Paginated job list", "#/components/schemas/PaginatedJobs"),
					},
				},
			},
			"/api/v1/queue/{id}": map[string]any{
				"get": map[string]any{
					"summary":     "Get a job",
					"operationId": "getJob",
					"parameters":  []map[string]any{pathParam("id", "Job id")},
					"responses": map[string]any{
						"200": resp("The job with items and outcomes", "#/components/schemas/Job"),
						"404": resp("Job not found", "#/components/schemas/Error"),
					},
				},
				"delete": map[string]any{
					"summary":     "Cancel or remove a job",
					"operationId": "deleteJob",
					"parameters":  []map[string]any{pathParam("id", "Job id")},
					"responses": map[string]any{
						"204": map[string]any{"description": "Canceled (if running) or removed"},
						"404": resp("Job not found", "#/components/schemas/Error"),
					},
				},
			},
			"/api/v1/queue/{id}/retry": map[string]any{
				"post": map[string]any{
					"summary":     "Retry a finished job",
					"operationId": "retryJob",
					"parameters": []map[string]any{
						pathParam("id", "Job id"),
						queryParam("force", "Set to 1 to re-run with the gallery-dl archive bypassed, re-downloading already-fetched posts"),
					},
					"responses": map[string]any{
						"202": resp("Re-queued", "#/components/schemas/EnqueueResponse"),
						"404": resp("Job not found", "#/components/schemas/Error"),
						"409": resp("Job is not in a retryable state", "#/components/schemas/Error"),
					},
				},
			},
			"/api/v1/queue/{id}/continue": map[string]any{
				"post": map[string]any{
					"summary":     "Fetch the next window of a capped job",
					"operationId": "continueJob",
					"parameters":  []map[string]any{pathParam("id", "Job id")},
					"responses": map[string]any{
						"202": resp("Follow-up job queued for the next window", "#/components/schemas/EnqueueResponse"),
						"404": resp("Job not found", "#/components/schemas/Error"),
						"409": resp("Job was not capped, so there is no next window", "#/components/schemas/Error"),
					},
				},
			},
			"/api/v1/sites": map[string]any{
				"get": map[string]any{
					"summary":     "List supported sites",
					"operationId": "listSites",
					"parameters":  []map[string]any{queryParam("q", "Substring filter on category/subcategory")},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Supported sites (curated first)",
							"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"total": map[string]any{"type": "integer"},
									"sites": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Site"}},
								},
							}}},
						},
					},
				},
			},
			"/api/v1/sites/{name}/test": map[string]any{
				"post": map[string]any{
					"summary":     "Probe a site",
					"operationId": "testSite",
					"parameters": []map[string]any{
						pathParam("name", "gallery-dl category"),
						queryParam("url", "URL to probe; defaults to the site's example URL"),
					},
					"responses": map[string]any{
						"200": resp("Probe result", "#/components/schemas/ProbeResult"),
						"404": resp("Unknown site and no url supplied", "#/components/schemas/Error"),
					},
				},
			},
			"/api/v1/openapi.json": map[string]any{
				"get": map[string]any{
					"summary":     "This OpenAPI document",
					"operationId": "openapi",
					"security":    []map[string]any{},
					"responses":   map[string]any{"200": map[string]any{"description": "OpenAPI 3 spec"}},
				},
			},
			"/api/v1/docs": map[string]any{
				"get": map[string]any{
					"summary":     "HTML API reference",
					"operationId": "docs",
					"security":    []map[string]any{},
					"responses":   map[string]any{"200": map[string]any{"description": "HTML reference"}},
				},
			},
		},
	}
}

func resp(desc, ref string) map[string]any {
	return map[string]any{"description": desc, "content": jsonContent(ref)}
}

func jsonBodySchema(required bool, schema map[string]any) map[string]any {
	return map[string]any{
		"required": required,
		"content":  map[string]any{"application/json": map[string]any{"schema": schema}},
	}
}

func jsonContent(ref string) map[string]any {
	return map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": ref}}}
}

func pathParam(name, desc string) map[string]any {
	return map[string]any{"name": name, "in": "path", "required": true, "description": desc, "schema": map[string]any{"type": "string"}}
}

func queryParam(name, desc string) map[string]any {
	return map[string]any{"name": name, "in": "query", "required": false, "description": desc, "schema": map[string]any{"type": "string"}}
}

// openAPIJSON serves the raw spec (no auth, so it sets CORS itself for the
// browser extension that fetches it cross-origin).
func (h *Handler) openAPIJSON(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(buildSpec(h.cfg.Current().Server.BaseURL))
}

// openAPIDocs renders the spec as a self-contained HTML reference (no auth, no
// external assets, so it sets CORS itself like openAPIJSON).
func (h *Handler) openAPIDocs(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	view := extractDocsView(buildSpec(h.cfg.Current().Server.BaseURL))
	view.APIProtected = h.cfg.Current().Auth.APIToken != ""
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := docsTemplate.Execute(w, view); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

type docsView struct {
	Title        string
	Version      string
	BaseURL      string
	APIProtected bool
	Endpoints    []endpointView
	Schemas      []schemaView
}

type endpointView struct {
	Method, MethodLower, Path, Summary, Anchor string
	Params                                     []paramView
	Request                                    *requestView
	Responses                                  []responseView
}

type paramView struct {
	Name, In, Description string
	Required              bool
}

type requestView struct {
	MediaTypes []mediaTypeView
}

type mediaTypeView struct {
	ContentType string
	Required    []string
	Properties  []propertyView
	Ref         string
	RefAnchor   string
}

type propertyView struct {
	Name, Type, Description string
}

type responseView struct {
	Status, Description, Ref, RefAnchor string
}

type schemaView struct {
	Name, Anchor string
	Properties   []propertyView
}

var methodOrder = []string{"get", "post", "put", "patch", "delete"}

func extractDocsView(spec map[string]any) docsView {
	view := docsView{}
	if info, ok := spec["info"].(map[string]any); ok {
		view.Title, _ = info["title"].(string)
		view.Version, _ = info["version"].(string)
	}
	if servers, ok := spec["servers"].([]map[string]any); ok && len(servers) > 0 {
		view.BaseURL, _ = servers[0]["url"].(string)
	}

	paths, _ := spec["paths"].(map[string]any)
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		methods, _ := paths[p].(map[string]any)
		for _, m := range methodOrder {
			op, ok := methods[m].(map[string]any)
			if !ok {
				continue
			}
			e := endpointView{Method: strings.ToUpper(m), MethodLower: m, Path: p, Anchor: m + "-" + anchorize(p)}
			e.Summary, _ = op["summary"].(string)
			if params, ok := op["parameters"].([]map[string]any); ok {
				for _, pp := range params {
					name, _ := pp["name"].(string)
					in, _ := pp["in"].(string)
					desc, _ := pp["description"].(string)
					req, _ := pp["required"].(bool)
					e.Params = append(e.Params, paramView{Name: name, In: in, Description: desc, Required: req})
				}
			}
			if body, ok := op["requestBody"].(map[string]any); ok {
				e.Request = extractRequest(body)
			}
			if resps, ok := op["responses"].(map[string]any); ok {
				e.Responses = extractResponses(resps)
			}
			view.Endpoints = append(view.Endpoints, e)
		}
	}

	if comps, ok := spec["components"].(map[string]any); ok {
		if schemas, ok := comps["schemas"].(map[string]any); ok {
			for _, n := range slices.Sorted(maps.Keys(schemas)) {
				s, _ := schemas[n].(map[string]any)
				sv := schemaView{Name: n, Anchor: anchorize(n)}
				if props, ok := s["properties"].(map[string]any); ok {
					sv.Properties = extractProps(props)
				}
				view.Schemas = append(view.Schemas, sv)
			}
		}
	}
	return view
}

func extractRequest(body map[string]any) *requestView {
	rv := &requestView{}
	content, ok := body["content"].(map[string]any)
	if !ok {
		return rv
	}
	for _, ct := range slices.Sorted(maps.Keys(content)) {
		mt, _ := content[ct].(map[string]any)
		schema, _ := mt["schema"].(map[string]any)
		mtv := mediaTypeView{ContentType: ct}
		if ref, ok := schema["$ref"].(string); ok {
			mtv.Ref = strings.TrimPrefix(ref, "#/components/schemas/")
			mtv.RefAnchor = anchorize(mtv.Ref)
		} else {
			if req, ok := schema["required"].([]string); ok {
				mtv.Required = req
			}
			if props, ok := schema["properties"].(map[string]any); ok {
				mtv.Properties = extractProps(props)
			}
		}
		rv.MediaTypes = append(rv.MediaTypes, mtv)
	}
	return rv
}

func extractResponses(resps map[string]any) []responseView {
	out := make([]responseView, 0, len(resps))
	for _, c := range slices.Sorted(maps.Keys(resps)) {
		r, _ := resps[c].(map[string]any)
		rv := responseView{Status: c}
		rv.Description, _ = r["description"].(string)
		if content, ok := r["content"].(map[string]any); ok {
			if app, ok := content["application/json"].(map[string]any); ok {
				if schema, ok := app["schema"].(map[string]any); ok {
					if ref, ok := schema["$ref"].(string); ok {
						rv.Ref = strings.TrimPrefix(ref, "#/components/schemas/")
						rv.RefAnchor = anchorize(rv.Ref)
					}
				}
			}
		}
		out = append(out, rv)
	}
	return out
}

func extractProps(props map[string]any) []propertyView {
	out := make([]propertyView, 0, len(props))
	for _, n := range slices.Sorted(maps.Keys(props)) {
		p, _ := props[n].(map[string]any)
		t, _ := p["type"].(string)
		d, _ := p["description"].(string)
		out = append(out, propertyView{Name: n, Type: t, Description: d})
	}
	return out
}

func anchorize(s string) string {
	r := strings.ToLower(s)
	r = strings.ReplaceAll(r, "/", "-")
	r = strings.ReplaceAll(r, "{", "")
	r = strings.ReplaceAll(r, "}", "")
	r = strings.ReplaceAll(r, ".", "-")
	r = strings.Trim(r, "-")
	if r == "" {
		r = "root"
	}
	return r
}

// docsTemplate renders the API reference with inline CSS in the indigo
// downloader palette.
var docsTemplate = template.Must(template.New("api-docs").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} - Docs</title>
<style>
 body { background:#0d0d0d; color:#c8c8c8; font-family:"JetBrains Mono","Fira Mono","Courier New",monospace; font-size:14px; line-height:1.5; padding:24px; max-width:1000px; margin:0 auto; }
 h1 { font-size:20px; font-weight:bold; margin-bottom:4px; }
 h2 { font-size:16px; color:#c8c8c8; border-bottom:1px solid #2a2a36; padding-bottom:4px; margin:24px 0 8px; }
 h3 { font-size:13px; color:#6a6a82; margin:12px 0 4px; font-weight:normal; text-transform:uppercase; letter-spacing:0.5px; }
 a { color:#5c6bc0; text-decoration:none; }
 a:hover { text-decoration:underline; }
 code { font-family:inherit; }
 table { border-collapse:collapse; width:100%; margin:6px 0 10px; font-size:13px; }
 th, td { border:1px solid #2a2a36; padding:4px 8px; text-align:left; vertical-align:top; }
 th { color:#6a6a82; font-weight:normal; background:#16161c; }
 .muted { color:#6a6a82; font-size:12px; }
 .method { display:inline-block; padding:1px 6px; border:1px solid; font-weight:bold; margin-right:8px; font-size:12px; min-width:52px; text-align:center; }
 .method-get    { color:#22aa44; border-color:#22aa44; }
 .method-post   { color:#5c6bc0; border-color:#5c6bc0; }
 .method-delete { color:#cc3333; border-color:#cc3333; }
 .path { color:#c8c8c8; }
 ul.toc { list-style:none; padding:0; margin:8px 0 20px; }
 ul.toc li { padding:2px 0; }
</style>
</head>
<body>
 <p class="muted"><a href="/">&larr; Back</a></p>
 <h1>{{.Title}}</h1>
 <p class="muted">Version {{.Version}} &middot; base URL <code>{{.BaseURL}}</code></p>
 {{if .APIProtected}}
 <p style="color:#22aa44;border:1px solid #22aa44;padding:4px 8px;">A bearer token is configured: send <code>Authorization: Bearer &lt;token&gt;</code> on every endpoint except <code>/health</code>, <code>/api/v1/openapi.json</code>, and <code>/api/v1/docs</code>.</p>
 {{else}}
 <p style="color:#ffaa00;border:1px solid #ffaa00;padding:4px 8px;">No bearer token is configured, so the API is open. Set <code>auth.api_token</code> to require <code>Authorization: Bearer &lt;token&gt;</code>.</p>
 {{end}}
 <p class="muted">Raw spec: <a href="/api/v1/openapi.json">openapi.json</a></p>

 <h2>Endpoints</h2>
 <ul class="toc">
 {{range .Endpoints}}
  <li><a href="#{{.Anchor}}"><span class="method method-{{.MethodLower}}">{{.Method}}</span><span class="path">{{.Path}}</span></a>{{if .Summary}} <span class="muted">- {{.Summary}}</span>{{end}}</li>
 {{end}}
 </ul>

 {{range .Endpoints}}
 <div class="endpoint">
  <h2 id="{{.Anchor}}"><span class="method method-{{.MethodLower}}">{{.Method}}</span><span class="path">{{.Path}}</span></h2>
  {{if .Summary}}<p>{{.Summary}}</p>{{end}}
  {{if .Params}}
  <h3>Parameters</h3>
  <table><thead><tr><th>Name</th><th>In</th><th>Required</th><th>Description</th></tr></thead><tbody>
  {{range .Params}}<tr><td><code>{{.Name}}</code></td><td>{{.In}}</td><td>{{if .Required}}yes{{else}}no{{end}}</td><td>{{.Description}}</td></tr>{{end}}
  </tbody></table>
  {{end}}
  {{if .Request}}
  <h3>Request body</h3>
  {{range .Request.MediaTypes}}
   <p class="muted">Content-Type: <code>{{.ContentType}}</code>{{if .Ref}} - schema <a href="#schema-{{.RefAnchor}}"><code>{{.Ref}}</code></a>{{end}}</p>
   {{if .Required}}<p class="muted">Required: {{range .Required}}<code>{{.}}</code> {{end}}</p>{{end}}
   {{if .Properties}}<table><thead><tr><th>Field</th><th>Type</th><th>Description</th></tr></thead><tbody>
   {{range .Properties}}<tr><td><code>{{.Name}}</code></td><td>{{.Type}}</td><td>{{.Description}}</td></tr>{{end}}
   </tbody></table>{{end}}
  {{end}}
  {{end}}
  {{if .Responses}}
  <h3>Responses</h3>
  <table><thead><tr><th>Status</th><th>Description</th><th>Schema</th></tr></thead><tbody>
  {{range .Responses}}<tr><td><code>{{.Status}}</code></td><td>{{.Description}}</td><td>{{if .Ref}}<a href="#schema-{{.RefAnchor}}"><code>{{.Ref}}</code></a>{{end}}</td></tr>{{end}}
  </tbody></table>
  {{end}}
 </div>
 {{end}}

 <h2>Schemas</h2>
 {{range .Schemas}}
  <h3 id="schema-{{.Anchor}}" style="color:#c8c8c8;font-size:14px;text-transform:none;letter-spacing:0;margin-top:14px">{{.Name}}</h3>
  {{if .Properties}}<table><thead><tr><th>Field</th><th>Type</th><th>Description</th></tr></thead><tbody>
  {{range .Properties}}<tr><td><code>{{.Name}}</code></td><td>{{.Type}}</td><td>{{.Description}}</td></tr>{{end}}
  </tbody></table>{{else}}<p class="muted">(no fields)</p>{{end}}
 {{end}}
</body>
</html>`))
