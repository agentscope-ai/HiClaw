package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/agentscope-ai/AgentTeams/agentteams-controller/internal/oss/ossfake"
	"github.com/agentscope-ai/AgentTeams/agentteams-controller/internal/service"
)

func writeSkill(t *testing.T, dir, name, description string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, name, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newSkillsRig builds a template tree:
//
//	base/worker-agent/skills/{file-sync,find-skills}
//	base/copaw-worker-agent/skills/{file-sync,task-progress}
//	base/team-leader-agent/skills/{leader-briefing}
//	(no hermes dir; a stray non-dir file in worker-agent/skills)
//
// and a shared OSS store:
//
//	agents/global/skills/shared-kb/SKILL.md   (directory entry → listed)
//	agents/global/skills/file-sync/SKILL.md   (collides with builtin → builtin wins)
//	agents/global/skills/notes.txt            (bare file → skipped)
//	agents/global/skills/.hidden/SKILL.md     (dot-entry → skipped)
func newSkillsRig(t *testing.T) (*SkillsHandler, *mcLikeOSS, string) {
	t.Helper()
	base := t.TempDir()
	writeSkill(t, filepath.Join(base, "worker-agent", "skills"), "file-sync", "Sync files with centralized storage.")
	writeSkill(t, filepath.Join(base, "worker-agent", "skills"), "find-skills", "Discover skills from the open ecosystem.")
	if err := os.WriteFile(filepath.Join(base, "worker-agent", "skills", "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(base, "copaw-worker-agent", "skills"), "file-sync", "Sync files with centralized storage (copaw).")
	writeSkill(t, filepath.Join(base, "copaw-worker-agent", "skills"), "task-progress", "Report task progress.")
	writeSkill(t, filepath.Join(base, "team-leader-agent", "skills"), "leader-briefing", "Brief team members.")

	store := ossfake.NewMemory()
	mustPut := func(key string) {
		t.Helper()
		if err := store.PutObject(context.Background(), key, []byte("---\nname: x\n---\n")); err != nil {
			t.Fatal(err)
		}
	}
	mustPut(globalSkillsPrefix + "shared-kb/SKILL.md")
	mustPut(globalSkillsPrefix + "file-sync/SKILL.md")
	mustPut(globalSkillsPrefix + "notes.txt")
	mustPut(globalSkillsPrefix + ".hidden/SKILL.md")

	fakeOSS := &mcLikeOSS{Memory: store}
	dir := filepath.Join(base, "worker-agent")
	return NewSkillsHandler(dir, fakeOSS), fakeOSS, base
}

func getSkills(t *testing.T, h *SkillsHandler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
	rec := httptest.NewRecorder()
	h.ListSkills(rec, req)
	return rec
}

func decodeSkills(t *testing.T, rec *httptest.ResponseRecorder) []SkillInfo {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp SkillListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Skills
}

func skillByName(t *testing.T, skills []SkillInfo, name string) SkillInfo {
	t.Helper()
	for _, s := range skills {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("skill %q not in catalog: %v", name, skills)
	return SkillInfo{}
}

// TestSkillsCatalogGolden covers the builtin half (per-runtime availability
// derived from service.BuiltinAgentDir) and the shared half (directory
// entries under agents/global/skills/ only).
func TestSkillsCatalogGolden(t *testing.T) {
	h, _, _ := newSkillsRig(t)
	skills := decodeSkills(t, getSkills(t, h))

	wantNames := []string{"file-sync", "find-skills", "leader-briefing", "shared-kb", "task-progress"}
	var gotNames []string
	for _, s := range skills {
		gotNames = append(gotNames, s.Name)
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("names = %v, want %v", gotNames, wantNames)
	}

	// builtin provided by two templates → union of both templates' runtimes
	// (default worker template serves every runtime except copaw/hermes;
	// copaw template serves copaw; hermes dir absent so hermes is missing)
	fs := skillByName(t, skills, "file-sync")
	if fs.Source != "builtin" {
		t.Errorf("file-sync source = %q, want builtin", fs.Source)
	}
	wantAgents := []string{"copaw-worker-agent", "worker-agent"}
	if !reflect.DeepEqual(fs.Agents, wantAgents) {
		t.Errorf("file-sync agents = %v, want %v", fs.Agents, wantAgents)
	}
	wantRuntimes := []string{"copaw", "deepseek-harness", "openclaw", "openhuman", "qwenpaw"}
	if !reflect.DeepEqual(fs.Runtimes, wantRuntimes) {
		t.Errorf("file-sync runtimes = %v, want %v", fs.Runtimes, wantRuntimes)
	}

	fsk := skillByName(t, skills, "find-skills")
	if want := []string{"deepseek-harness", "openclaw", "openhuman", "qwenpaw"}; !reflect.DeepEqual(fsk.Runtimes, want) {
		t.Errorf("find-skills runtimes = %v, want %v", fsk.Runtimes, want)
	}

	tp := skillByName(t, skills, "task-progress")
	if want := []string{"copaw"}; !reflect.DeepEqual(tp.Runtimes, want) {
		t.Errorf("task-progress runtimes = %v, want %v", tp.Runtimes, want)
	}

	// leader template serves every runtime (leader role exists on all runtimes)
	lb := skillByName(t, skills, "leader-briefing")
	if want := append([]string{}, service.AllWorkerRuntimes...); !reflect.DeepEqual(lb.Runtimes, sortedCopy(want)) {
		t.Errorf("leader-briefing runtimes = %v, want %v", lb.Runtimes, sortedCopy(want))
	}
	if len(lb.Agents) != 1 || lb.Agents[0] != "team-leader-agent" {
		t.Errorf("leader-briefing agents = %v, want [team-leader-agent]", lb.Agents)
	}

	// shared half: directory entry only; builtin name wins on collision
	sk := skillByName(t, skills, "shared-kb")
	if sk.Source != "shared" {
		t.Errorf("shared-kb source = %q, want shared", sk.Source)
	}
	if !reflect.DeepEqual(sk.Runtimes, sortedCopy(append([]string{}, service.AllWorkerRuntimes...))) {
		t.Errorf("shared-kb runtimes = %v, want all runtimes", sk.Runtimes)
	}
}

// TestSkillsCatalogMappingConsistency pins the catalog's template→runtime
// derivation to service.BuiltinAgentDir: for every (role, runtime) pair the
// catalog must credit exactly the template the Deployer would seed from.
func TestSkillsCatalogMappingConsistency(t *testing.T) {
	h, _, _ := newSkillsRig(t)
	templates := h.builtinTemplates()
	byRuntime := map[string]map[string]bool{} // runtime → set of template dirs
	for _, tmpl := range templates {
		for _, rt := range tmpl.runtimes {
			if byRuntime[rt] == nil {
				byRuntime[rt] = map[string]bool{}
			}
			byRuntime[rt][tmpl.dir] = true
		}
	}
	for _, role := range []string{"worker", "team_leader"} {
		for _, rt := range service.AllWorkerRuntimes {
			want := service.BuiltinAgentDir(h.workerAgentDir, role, rt)
			if !byRuntime[rt][want] {
				t.Errorf("(role=%s, runtime=%s): BuiltinAgentDir = %s, but catalog does not credit it for %s",
					role, rt, want, rt)
			}
		}
	}
}

// TestSkillsCatalogFieldDiscipline asserts the response schema carries
// identity/availability only — no content, credential, or registry fields
// can sneak in.
func TestSkillsCatalogFieldDiscipline(t *testing.T) {
	h, _, _ := newSkillsRig(t)
	rec := getSkills(t, h)
	var payload struct {
		Skills []map[string]any `json:"skills"`
		Total  int              `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Total != len(payload.Skills) {
		t.Fatalf("total = %d, entries = %d", payload.Total, len(payload.Skills))
	}
	allowed := map[string]bool{"name": true, "description": true, "source": true, "agents": true, "runtimes": true}
	for _, entry := range payload.Skills {
		for k := range entry {
			if !allowed[k] {
				t.Errorf("unexpected field %q in catalog entry %v", k, entry)
			}
		}
	}
}

// TestSkillsCatalogSharedDegradesOnOSSFailure: a listing failure degrades to
// an empty shared half; the catalog still serves builtins with 200.
func TestSkillsCatalogSharedDegradesOnOSSFailure(t *testing.T) {
	base := t.TempDir()
	writeSkill(t, filepath.Join(base, "worker-agent", "skills"), "file-sync", "Sync files.")
	failing := &mcLikeOSS{Memory: ossfake.NewMemory(), failList: true}
	h := NewSkillsHandler(filepath.Join(base, "worker-agent"), failing)

	skills := decodeSkills(t, getSkills(t, h))
	if len(skills) != 1 || skills[0].Name != "file-sync" || skills[0].Source != "builtin" {
		t.Fatalf("skills = %v, want builtin-only [file-sync]", skills)
	}
}

// TestSkillsCatalogNoTemplateDir: an empty workerAgentDir yields no builtins
// but still lists shared skills.
func TestSkillsCatalogNoTemplateDir(t *testing.T) {
	store := ossfake.NewMemory()
	if err := store.PutObject(context.Background(), globalSkillsPrefix+"shared-kb/SKILL.md", []byte("x")); err != nil {
		t.Fatal(err)
	}
	h := NewSkillsHandler("", &mcLikeOSS{Memory: store})
	skills := decodeSkills(t, getSkills(t, h))
	if len(skills) != 1 || skills[0].Name != "shared-kb" || skills[0].Source != "shared" {
		t.Fatalf("skills = %v, want shared-only [shared-kb]", skills)
	}
}

func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	// simple insertion sort keeps the test dependency-free
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
