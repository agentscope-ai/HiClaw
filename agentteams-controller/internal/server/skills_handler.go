package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agentscope-ai/AgentTeams/agentteams-controller/internal/httputil"
	"github.com/agentscope-ai/AgentTeams/agentteams-controller/internal/oss"
	"github.com/agentscope-ai/AgentTeams/agentteams-controller/internal/service"
)

// globalSkillsPrefix is the deployment-wide skill staging area maintained by
// the dashboard's skill-upload flow. Listing it read-only gives the catalog
// its "shared" half — the set of skills available for distribution across
// the deployment.
//
// Retention semantics: no worker entrypoint consumes this prefix
// automatically. A shared skill reaches a worker only through per-worker
// distribution (dashboard Worker dialog, or L1/L2 PUT /workers skills),
// which also records the assignment in spec.skills. Deleting
// agents/global/skills/{name}/ removes the skill from the catalog (and the
// dashboard's global area); already-distributed per-worker copies
// (agents/<worker>/skills/{name}/) and existing spec.skills assignments are
// NOT touched — there is no cascade.
const globalSkillsPrefix = "agents/global/skills/"

// SkillInfo is one entry of the read-only skill catalog. It carries
// identity/availability only — never skill content, credentials, or
// registry connection details.
type SkillInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Source      string   `json:"source"` // "builtin" | "shared"
	Agents      []string `json:"agents,omitempty"`   // builtin only: template dirs providing the skill
	Runtimes    []string `json:"runtimes,omitempty"` // runtimes for which the skill is available
}

// SkillListResponse is the payload of GET /api/v1/skills.
type SkillListResponse struct {
	Skills []SkillInfo `json:"skills"`
	Total  int         `json:"total"`
}

// SkillsHandler serves the read-only skill catalog: built-in skills from the
// controller's agent template directories (availability per runtime derived
// from service.BuiltinAgentDir, the same function the Deployer uses to seed
// workers) plus the deployment-wide shared skills staged under
// agents/global/skills/. It never reads skill content beyond the SKILL.md
// frontmatter (name/description) of builtin skills.
type SkillsHandler struct {
	workerAgentDir string
	oss            oss.StorageClient
}

func NewSkillsHandler(workerAgentDir string, o oss.StorageClient) *SkillsHandler {
	return &SkillsHandler{workerAgentDir: workerAgentDir, oss: o}
}

// ListSkills handles GET /api/v1/skills.
func (h *SkillsHandler) ListSkills(w http.ResponseWriter, r *http.Request) {
	skills := map[string]*SkillInfo{}

	for _, tmpl := range h.builtinTemplates() {
		skillRoot := filepath.Join(tmpl.dir, "skills")
		entries, err := os.ReadDir(skillRoot)
		if err != nil {
			continue // missing template dir for this deployment
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name, description := parseSkillFrontmatter(filepath.Join(skillRoot, entry.Name(), "SKILL.md"))
			if name == "" {
				name = entry.Name()
			}
			if info, ok := skills[name]; ok {
				if info.Source == "builtin" {
					info.Agents = appendUniqueStrings(info.Agents, tmpl.dirName)
					info.Runtimes = unionSorted(info.Runtimes, tmpl.runtimes)
					if info.Description == "" {
						info.Description = description
					}
				}
				continue
			}
			skills[name] = &SkillInfo{
				Name:        name,
				Description: description,
				Source:      "builtin",
				Agents:      []string{tmpl.dirName},
				Runtimes:    append([]string{}, tmpl.runtimes...),
			}
		}
	}

	// Shared half: directory entries under agents/global/skills/. A listing
	// failure (prefix absent, storage down) degrades to an empty shared set
	// rather than failing the whole catalog. Directory entries only (mc ls
	// marks them with a trailing "/"); bare files and dot-entries are
	// non-skill artifacts. Builtin names win on collision.
	if h.oss != nil {
		if entries, err := h.oss.ListObjects(r.Context(), globalSkillsPrefix); err == nil {
			for _, raw := range entries {
				if !strings.HasSuffix(raw, "/") {
					continue
				}
				name := strings.TrimSuffix(raw, "/")
				if name == "" || strings.HasPrefix(name, ".") {
					continue
				}
				if _, ok := skills[name]; ok {
					continue
				}
				skills[name] = &SkillInfo{
					Name:     name,
					Source:   "shared",
					Runtimes: append([]string{}, service.AllWorkerRuntimes...),
				}
			}
		}
	}

	list := make([]SkillInfo, 0, len(skills))
	for _, info := range skills {
		sort.Strings(info.Agents)
		sort.Strings(info.Runtimes)
		list = append(list, *info)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })

	httputil.WriteJSON(w, http.StatusOK, SkillListResponse{Skills: list, Total: len(list)})
}

// builtinTemplate is one template directory and the runtimes it seeds.
type builtinTemplate struct {
	dir      string // absolute template dir
	dirName  string // directory name (as reported in SkillInfo.Agents)
	runtimes []string
}

// builtinTemplates derives the template→runtime mapping from
// service.BuiltinAgentDir — the same function the Deployer uses to seed
// workers — so the catalog's per-runtime availability can never drift from
// what workers actually receive.
func (h *SkillsHandler) builtinTemplates() []builtinTemplate {
	if h.workerAgentDir == "" {
		return nil
	}
	byDir := map[string]*builtinTemplate{}
	order := []string{}
	bucket := func(dir string) *builtinTemplate {
		b, ok := byDir[dir]
		if !ok {
			b = &builtinTemplate{dir: dir, dirName: filepath.Base(dir)}
			byDir[dir] = b
			order = append(order, dir)
		}
		return b
	}
	for _, rt := range service.AllWorkerRuntimes {
		workerTmpl := bucket(service.BuiltinAgentDir(h.workerAgentDir, "worker", rt))
		workerTmpl.runtimes = appendUniqueStrings(workerTmpl.runtimes, rt)
		leaderTmpl := bucket(service.BuiltinAgentDir(h.workerAgentDir, "team_leader", rt))
		leaderTmpl.runtimes = appendUniqueStrings(leaderTmpl.runtimes, rt)
	}
	out := make([]builtinTemplate, 0, len(order))
	for _, dir := range order {
		t := byDir[dir]
		sort.Strings(t.runtimes)
		out = append(out, *t)
	}
	return out
}

// parseSkillFrontmatter extracts name/description from the YAML frontmatter
// of a SKILL.md. Returns ("", "") when the file or frontmatter is missing —
// callers fall back to the directory name.
func parseSkillFrontmatter(path string) (name, description string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		if v, ok := strings.CutPrefix(trimmed, "name:"); ok {
			name = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(trimmed, "description:"); ok {
			description = strings.TrimSpace(v)
		}
	}
	return name, description
}

func appendUniqueStrings(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}

// unionSorted merges two string slices, dropping duplicates.
func unionSorted(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(append([]string{}, a...), b...) {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
