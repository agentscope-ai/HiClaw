package server

import (
	"context"
	"encoding/json"
	"net/http"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	authpkg "github.com/hiclaw/hiclaw-controller/internal/auth"
	"github.com/hiclaw/hiclaw-controller/internal/httputil"
	hiclawwebhook "github.com/hiclaw/hiclaw-controller/internal/webhook"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResourceHandler handles declarative CRUD operations on CRs. All create /
// update paths invoke the shared admission validators inline so that
// embedded mode (which does not run a webhook server) still enforces the
// same structural invariants as incluster mode.
type ResourceHandler struct {
	client     client.Client
	namespace  string
	validators *hiclawwebhook.Validators
}

// NewResourceHandler constructs a ResourceHandler. The validators argument
// may be nil in tests that do not exercise the validation path; all handler
// methods treat a nil Validators (or nested nil sub-validator) as a no-op.
func NewResourceHandler(c client.Client, namespace string, v *hiclawwebhook.Validators) *ResourceHandler {
	return &ResourceHandler{client: c, namespace: namespace, validators: v}
}

// --- Workers ---

func (h *ResourceHandler) CreateWorker(w http.ResponseWriter, r *http.Request) {
	var req CreateWorkerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}

	worker := &v1beta1.Worker{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: h.namespace,
		},
		Spec: v1beta1.WorkerSpec{
			Model:         req.Model,
			Runtime:       req.Runtime,
			Image:         req.Image,
			Role:          req.Role,
			TeamRef:       req.TeamRef,
			Identity:      req.Identity,
			Soul:          req.Soul,
			Agents:        req.Agents,
			Skills:        req.Skills,
			McpServers:    req.McpServers,
			Package:       req.Package,
			Expose:        req.Expose,
			ChannelPolicy: req.ChannelPolicy,
			State:         req.State,
		},
	}

	// A team-leader caller may only create workers inside their own team,
	// and only as members. Force-override whatever the client supplied.
	caller := authpkg.CallerFromContext(r.Context())
	if caller != nil && caller.Role == authpkg.RoleTeamLeader {
		worker.Spec.TeamRef = caller.Team
		worker.Spec.Role = v1beta1.WorkerRoleTeamWorker
	}

	if errs := h.validateWorker(r.Context(), worker, nil); len(errs) > 0 {
		writeValidationError(w, errs)
		return
	}

	if err := h.client.Create(r.Context(), worker); err != nil {
		writeK8sError(w, "create worker", err)
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, workerToResponse(worker))
}

func (h *ResourceHandler) GetWorker(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker name is required")
		return
	}

	var worker v1beta1.Worker
	if err := h.client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &worker); err != nil {
		writeK8sError(w, "get worker", err)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, workerToResponse(&worker))
}

func (h *ResourceHandler) ListWorkers(w http.ResponseWriter, r *http.Request) {
	var list v1beta1.WorkerList
	opts := []client.ListOption{client.InNamespace(h.namespace)}

	team := r.URL.Query().Get("team")
	if team != "" {
		opts = append(opts, client.MatchingLabels{v1beta1.LabelTeam: team})
	}

	if err := h.client.List(r.Context(), &list, opts...); err != nil {
		writeK8sError(w, "list workers", err)
		return
	}

	workers := make([]WorkerResponse, 0, len(list.Items))
	for i := range list.Items {
		workers = append(workers, workerToResponse(&list.Items[i]))
	}

	httputil.WriteJSON(w, http.StatusOK, WorkerListResponse{Workers: workers, Total: len(workers)})
}

func (h *ResourceHandler) UpdateWorker(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker name is required")
		return
	}

	var req UpdateWorkerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	var existing v1beta1.Worker
	if err := h.client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &existing); err != nil {
		writeK8sError(w, "get worker for update", err)
		return
	}
	oldWorker := existing.DeepCopy()
	newWorker := &existing

	if req.Model != "" {
		newWorker.Spec.Model = req.Model
	}
	if req.Runtime != "" {
		newWorker.Spec.Runtime = req.Runtime
	}
	if req.Image != "" {
		newWorker.Spec.Image = req.Image
	}
	if req.Identity != "" {
		newWorker.Spec.Identity = req.Identity
	}
	if req.Soul != "" {
		newWorker.Spec.Soul = req.Soul
	}
	if req.Agents != "" {
		newWorker.Spec.Agents = req.Agents
	}
	if req.Skills != nil {
		newWorker.Spec.Skills = req.Skills
	}
	if req.McpServers != nil {
		newWorker.Spec.McpServers = req.McpServers
	}
	if req.Package != "" {
		newWorker.Spec.Package = req.Package
	}
	if req.Expose != nil {
		newWorker.Spec.Expose = req.Expose
	}
	if req.ChannelPolicy != nil {
		newWorker.Spec.ChannelPolicy = req.ChannelPolicy
	}
	if req.State != nil {
		newWorker.Spec.State = req.State
	}
	if req.Role != "" {
		newWorker.Spec.Role = req.Role
	}
	if req.TeamRef != nil {
		newWorker.Spec.TeamRef = *req.TeamRef
	}

	if errs := h.validateWorker(r.Context(), newWorker, oldWorker); len(errs) > 0 {
		writeValidationError(w, errs)
		return
	}

	if err := h.client.Update(r.Context(), newWorker); err != nil {
		writeK8sError(w, "update worker", err)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, workerToResponse(newWorker))
}

func (h *ResourceHandler) DeleteWorker(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker name is required")
		return
	}

	worker := &v1beta1.Worker{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: h.namespace},
	}
	if err := h.client.Delete(r.Context(), worker); err != nil {
		writeK8sError(w, "delete worker", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Teams ---

func (h *ResourceHandler) CreateTeam(w http.ResponseWriter, r *http.Request) {
	var req CreateTeamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}

	team := &v1beta1.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: h.namespace,
		},
		Spec: v1beta1.TeamSpec{
			Description:       req.Description,
			PeerMentions:      req.PeerMentions,
			ChannelPolicy:     req.ChannelPolicy,
			Heartbeat:         req.Heartbeat,
			WorkerIdleTimeout: req.WorkerIdleTimeout,
		},
	}

	if errs := h.validateTeam(r.Context(), team, nil); len(errs) > 0 {
		writeValidationError(w, errs)
		return
	}

	if err := h.client.Create(r.Context(), team); err != nil {
		writeK8sError(w, "create team", err)
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, teamToResponse(team))
}

func (h *ResourceHandler) GetTeam(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "team name is required")
		return
	}

	var team v1beta1.Team
	if err := h.client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &team); err != nil {
		writeK8sError(w, "get team", err)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, teamToResponse(&team))
}

func (h *ResourceHandler) ListTeams(w http.ResponseWriter, r *http.Request) {
	var list v1beta1.TeamList
	if err := h.client.List(r.Context(), &list, client.InNamespace(h.namespace)); err != nil {
		writeK8sError(w, "list teams", err)
		return
	}

	teams := make([]TeamResponse, 0, len(list.Items))
	for i := range list.Items {
		teams = append(teams, teamToResponse(&list.Items[i]))
	}

	httputil.WriteJSON(w, http.StatusOK, TeamListResponse{Teams: teams, Total: len(teams)})
}

func (h *ResourceHandler) UpdateTeam(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "team name is required")
		return
	}

	var req UpdateTeamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	var existing v1beta1.Team
	if err := h.client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &existing); err != nil {
		writeK8sError(w, "get team for update", err)
		return
	}
	oldTeam := existing.DeepCopy()
	newTeam := &existing

	if req.Description != "" {
		newTeam.Spec.Description = req.Description
	}
	if req.PeerMentions != nil {
		newTeam.Spec.PeerMentions = req.PeerMentions
	}
	if req.ChannelPolicy != nil {
		newTeam.Spec.ChannelPolicy = req.ChannelPolicy
	}
	if req.Heartbeat != nil {
		newTeam.Spec.Heartbeat = req.Heartbeat
	}
	if req.WorkerIdleTimeout != "" {
		newTeam.Spec.WorkerIdleTimeout = req.WorkerIdleTimeout
	}

	if errs := h.validateTeam(r.Context(), newTeam, oldTeam); len(errs) > 0 {
		writeValidationError(w, errs)
		return
	}

	if err := h.client.Update(r.Context(), newTeam); err != nil {
		writeK8sError(w, "update team", err)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, teamToResponse(newTeam))
}

func (h *ResourceHandler) DeleteTeam(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "team name is required")
		return
	}

	team := &v1beta1.Team{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: h.namespace},
	}
	if err := h.client.Delete(r.Context(), team); err != nil {
		writeK8sError(w, "delete team", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Humans ---

func (h *ResourceHandler) CreateHuman(w http.ResponseWriter, r *http.Request) {
	var req CreateHumanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}

	human := &v1beta1.Human{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: h.namespace,
		},
		Spec: v1beta1.HumanSpec{
			DisplayName:  req.DisplayName,
			Email:        req.Email,
			Note:         req.Note,
			SuperAdmin:   req.SuperAdmin,
			TeamAccess:   req.TeamAccess,
			WorkerAccess: req.WorkerAccess,
		},
	}

	if errs := h.validateHuman(r.Context(), human, nil); len(errs) > 0 {
		writeValidationError(w, errs)
		return
	}

	if err := h.client.Create(r.Context(), human); err != nil {
		writeK8sError(w, "create human", err)
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, humanToResponse(human))
}

func (h *ResourceHandler) GetHuman(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "human name is required")
		return
	}

	var human v1beta1.Human
	if err := h.client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &human); err != nil {
		writeK8sError(w, "get human", err)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, humanToResponse(&human))
}

func (h *ResourceHandler) ListHumans(w http.ResponseWriter, r *http.Request) {
	var list v1beta1.HumanList
	if err := h.client.List(r.Context(), &list, client.InNamespace(h.namespace)); err != nil {
		writeK8sError(w, "list humans", err)
		return
	}

	humans := make([]HumanResponse, 0, len(list.Items))
	for i := range list.Items {
		humans = append(humans, humanToResponse(&list.Items[i]))
	}

	httputil.WriteJSON(w, http.StatusOK, HumanListResponse{Humans: humans, Total: len(humans)})
}

func (h *ResourceHandler) UpdateHuman(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "human name is required")
		return
	}

	var req UpdateHumanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	var existing v1beta1.Human
	if err := h.client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &existing); err != nil {
		writeK8sError(w, "get human for update", err)
		return
	}
	oldHuman := existing.DeepCopy()
	newHuman := &existing

	if req.DisplayName != "" {
		newHuman.Spec.DisplayName = req.DisplayName
	}
	if req.Email != "" {
		newHuman.Spec.Email = req.Email
	}
	if req.Note != "" {
		newHuman.Spec.Note = req.Note
	}
	if req.SuperAdmin != nil {
		newHuman.Spec.SuperAdmin = *req.SuperAdmin
	}
	if req.TeamAccess != nil {
		newHuman.Spec.TeamAccess = req.TeamAccess
	}
	if req.WorkerAccess != nil {
		newHuman.Spec.WorkerAccess = req.WorkerAccess
	}

	if errs := h.validateHuman(r.Context(), newHuman, oldHuman); len(errs) > 0 {
		writeValidationError(w, errs)
		return
	}

	if err := h.client.Update(r.Context(), newHuman); err != nil {
		writeK8sError(w, "update human", err)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, humanToResponse(newHuman))
}

func (h *ResourceHandler) DeleteHuman(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "human name is required")
		return
	}

	human := &v1beta1.Human{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: h.namespace},
	}
	if err := h.client.Delete(r.Context(), human); err != nil {
		writeK8sError(w, "delete human", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Managers ---

func (h *ResourceHandler) CreateManager(w http.ResponseWriter, r *http.Request) {
	var req CreateManagerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Model == "" {
		httputil.WriteError(w, http.StatusBadRequest, "model is required")
		return
	}

	mgr := &v1beta1.Manager{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: h.namespace,
		},
		Spec: v1beta1.ManagerSpec{
			Model:      req.Model,
			Runtime:    req.Runtime,
			Image:      req.Image,
			Soul:       req.Soul,
			Agents:     req.Agents,
			Skills:     req.Skills,
			McpServers: req.McpServers,
			Package:    req.Package,
			State:      req.State,
		},
	}
	if req.Config != nil {
		mgr.Spec.Config = *req.Config
	}

	if err := h.client.Create(r.Context(), mgr); err != nil {
		writeK8sError(w, "create manager", err)
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, managerToResponse(mgr))
}

func (h *ResourceHandler) GetManager(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "manager name is required")
		return
	}

	var mgr v1beta1.Manager
	if err := h.client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &mgr); err != nil {
		writeK8sError(w, "get manager", err)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, managerToResponse(&mgr))
}

func (h *ResourceHandler) ListManagers(w http.ResponseWriter, r *http.Request) {
	var list v1beta1.ManagerList
	if err := h.client.List(r.Context(), &list, client.InNamespace(h.namespace)); err != nil {
		writeK8sError(w, "list managers", err)
		return
	}

	managers := make([]ManagerResponse, 0, len(list.Items))
	for i := range list.Items {
		managers = append(managers, managerToResponse(&list.Items[i]))
	}

	httputil.WriteJSON(w, http.StatusOK, ManagerListResponse{Managers: managers, Total: len(managers)})
}

func (h *ResourceHandler) UpdateManager(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "manager name is required")
		return
	}

	var req UpdateManagerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	var mgr v1beta1.Manager
	if err := h.client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &mgr); err != nil {
		writeK8sError(w, "get manager for update", err)
		return
	}

	if req.Model != "" {
		mgr.Spec.Model = req.Model
	}
	if req.Runtime != "" {
		mgr.Spec.Runtime = req.Runtime
	}
	if req.Image != "" {
		mgr.Spec.Image = req.Image
	}
	if req.Soul != "" {
		mgr.Spec.Soul = req.Soul
	}
	if req.Agents != "" {
		mgr.Spec.Agents = req.Agents
	}
	if req.Skills != nil {
		mgr.Spec.Skills = req.Skills
	}
	if req.McpServers != nil {
		mgr.Spec.McpServers = req.McpServers
	}
	if req.Package != "" {
		mgr.Spec.Package = req.Package
	}
	if req.Config != nil {
		mgr.Spec.Config = *req.Config
	}
	if req.State != nil {
		mgr.Spec.State = req.State
	}

	if err := h.client.Update(r.Context(), &mgr); err != nil {
		writeK8sError(w, "update manager", err)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, managerToResponse(&mgr))
}

func (h *ResourceHandler) DeleteManager(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "manager name is required")
		return
	}

	mgr := &v1beta1.Manager{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: h.namespace},
	}
	if err := h.client.Delete(r.Context(), mgr); err != nil {
		writeK8sError(w, "delete manager", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Inline validator wrappers ---

func (h *ResourceHandler) validateWorker(ctx context.Context, newW, oldW *v1beta1.Worker) field.ErrorList {
	if h.validators == nil || h.validators.Worker == nil {
		return nil
	}
	return h.validators.Worker.ValidateWorker(ctx, newW, oldW)
}

func (h *ResourceHandler) validateTeam(ctx context.Context, newT, oldT *v1beta1.Team) field.ErrorList {
	if h.validators == nil || h.validators.Team == nil {
		return nil
	}
	return h.validators.Team.ValidateTeam(ctx, newT, oldT)
}

func (h *ResourceHandler) validateHuman(ctx context.Context, newH, oldH *v1beta1.Human) field.ErrorList {
	if h.validators == nil || h.validators.Human == nil {
		return nil
	}
	return h.validators.Human.ValidateHuman(ctx, newH, oldH)
}

// --- Conversion helpers ---

func workerToResponse(w *v1beta1.Worker) WorkerResponse {
	resp := WorkerResponse{
		Name:           w.Name,
		Phase:          w.Status.Phase,
		State:          w.Spec.DesiredState(),
		Model:          w.Spec.Model,
		Runtime:        w.Spec.Runtime,
		Image:          w.Spec.Image,
		ContainerState: w.Status.ContainerState,
		MatrixUserID:   w.Status.MatrixUserID,
		RoomID:         w.Status.RoomID,
		Message:        w.Status.Message,
		Role:           w.Spec.EffectiveRole(),
		TeamRef:        w.Spec.TeamRef,
	}
	if resp.Phase == "" {
		resp.Phase = "Pending"
	}
	for _, ep := range w.Status.ExposedPorts {
		resp.ExposedPorts = append(resp.ExposedPorts, ExposedPortInfo{Port: ep.Port, Domain: ep.Domain})
	}
	return resp
}

func teamToResponse(t *v1beta1.Team) TeamResponse {
	resp := TeamResponse{
		Name:              t.Name,
		Phase:             t.Status.Phase,
		Description:       t.Spec.Description,
		Heartbeat:         t.Spec.Heartbeat,
		WorkerIdleTimeout: t.Spec.WorkerIdleTimeout,
		TeamRoomID:        t.Status.TeamRoomID,
		LeaderDMRoomID:    t.Status.LeaderDMRoomID,
		TotalMembers:      t.Status.TotalMembers,
		ReadyMembers:      t.Status.ReadyMembers,
		Message:           t.Status.Message,
	}
	if resp.Phase == "" {
		resp.Phase = "Pending"
	}
	if t.Status.Leader != nil {
		resp.LeaderName = t.Status.Leader.Name
		resp.LeaderMatrixUserID = t.Status.Leader.MatrixUserID
		resp.LeaderReady = t.Status.Leader.Ready
	}
	for _, m := range t.Status.Members {
		resp.Members = append(resp.Members, TeamMemberInfo{
			Name:         m.Name,
			Role:         m.Role,
			MatrixUserID: m.MatrixUserID,
			Ready:        m.Ready,
		})
	}
	for _, a := range t.Status.Admins {
		resp.Admins = append(resp.Admins, TeamAdminInfo{
			HumanName:    a.HumanName,
			MatrixUserID: a.MatrixUserID,
		})
	}
	return resp
}

func managerToResponse(m *v1beta1.Manager) ManagerResponse {
	resp := ManagerResponse{
		Name:         m.Name,
		Phase:        m.Status.Phase,
		State:        m.Spec.DesiredState(),
		Model:        m.Spec.Model,
		Runtime:      m.Spec.Runtime,
		Image:        m.Spec.Image,
		MatrixUserID: m.Status.MatrixUserID,
		RoomID:       m.Status.RoomID,
		Version:      m.Status.Version,
		Message:      m.Status.Message,
	}
	if resp.Phase == "" {
		resp.Phase = "Pending"
	}
	return resp
}

func humanToResponse(h *v1beta1.Human) HumanResponse {
	resp := HumanResponse{
		Name:            h.Name,
		Phase:           h.Status.Phase,
		DisplayName:     h.Spec.DisplayName,
		Email:           h.Spec.Email,
		MatrixUserID:    h.Status.MatrixUserID,
		InitialPassword: h.Status.InitialPassword,
		Rooms:           h.Status.Rooms,
		SuperAdmin:      h.Spec.SuperAdmin,
		TeamAccess:      h.Spec.TeamAccess,
		WorkerAccess:    h.Spec.WorkerAccess,
		Message:         h.Status.Message,
	}
	if resp.Phase == "" {
		resp.Phase = "Pending"
	}
	return resp
}

// writeK8sError maps K8s API errors to HTTP status codes.
func writeK8sError(w http.ResponseWriter, op string, err error) {
	switch {
	case apierrors.IsNotFound(err):
		httputil.WriteError(w, http.StatusNotFound, op+": not found")
	case apierrors.IsAlreadyExists(err):
		httputil.WriteError(w, http.StatusConflict, op+": already exists")
	default:
		httputil.WriteError(w, http.StatusInternalServerError, op+": "+err.Error())
	}
}

// writeValidationError renders an admission-style field.ErrorList as a
// single 400 response with the aggregated error string.
func writeValidationError(w http.ResponseWriter, errs field.ErrorList) {
	httputil.WriteError(w, http.StatusBadRequest, "validation failed: "+errs.ToAggregate().Error())
}
