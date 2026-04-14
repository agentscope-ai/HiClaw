package server

import (
	"encoding/json"
	"net/http"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	authpkg "github.com/hiclaw/hiclaw-controller/internal/auth"
	"github.com/hiclaw/hiclaw-controller/internal/httputil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResourceHandler handles declarative CRUD operations on CRs.
type ResourceHandler struct {
	client    client.Client
	namespace string
}

func NewResourceHandler(c client.Client, namespace string) *ResourceHandler {
	return &ResourceHandler{client: c, namespace: namespace}
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

	caller := authpkg.CallerFromContext(r.Context())
	if caller != nil && caller.Role == authpkg.RoleTeamLeader {
		req.Team = caller.Team
		req.Role = "worker"
		req.TeamLeader = caller.Username
	}

	if req.Team != "" || req.Role != "" || req.TeamLeader != "" {
		worker.Annotations = make(map[string]string)
		if req.Team != "" {
			worker.Annotations["hiclaw.io/team"] = req.Team
		}
		if req.Role != "" {
			worker.Annotations["hiclaw.io/role"] = req.Role
		}
		if req.TeamLeader != "" {
			worker.Annotations["hiclaw.io/team-leader"] = req.TeamLeader
		}
		worker.Labels = map[string]string{}
		if req.Team != "" {
			worker.Labels["hiclaw.io/team"] = req.Team
		}
		if req.Role != "" {
			worker.Labels["hiclaw.io/role"] = req.Role
		}
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
		opts = append(opts, client.MatchingLabels{"hiclaw.io/team": team})
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

	var worker v1beta1.Worker
	if err := h.client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &worker); err != nil {
		writeK8sError(w, "get worker for update", err)
		return
	}

	if req.Model != "" {
		worker.Spec.Model = req.Model
	}
	if req.Runtime != "" {
		worker.Spec.Runtime = req.Runtime
	}
	if req.Image != "" {
		worker.Spec.Image = req.Image
	}
	if req.Identity != "" {
		worker.Spec.Identity = req.Identity
	}
	if req.Soul != "" {
		worker.Spec.Soul = req.Soul
	}
	if req.Agents != "" {
		worker.Spec.Agents = req.Agents
	}
	if req.Skills != nil {
		worker.Spec.Skills = req.Skills
	}
	if req.McpServers != nil {
		worker.Spec.McpServers = req.McpServers
	}
	if req.Package != "" {
		worker.Spec.Package = req.Package
	}
	if req.Expose != nil {
		worker.Spec.Expose = req.Expose
	}
	if req.ChannelPolicy != nil {
		worker.Spec.ChannelPolicy = req.ChannelPolicy
	}
	if req.State != nil {
		worker.Spec.State = req.State
	}

	if err := h.client.Update(r.Context(), &worker); err != nil {
		writeK8sError(w, "update worker", err)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, workerToResponse(&worker))
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
	if req.Leader.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "leader.name is required")
		return
	}

	team := &v1beta1.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: h.namespace,
		},
		Spec: v1beta1.TeamSpec{
			Description:   req.Description,
			Admin:         req.Admin,
			PeerMentions:  req.PeerMentions,
			ChannelPolicy: req.ChannelPolicy,
			Leader: v1beta1.LeaderSpec{
				Name:              req.Leader.Name,
				Model:             req.Leader.Model,
				Identity:          req.Leader.Identity,
				Soul:              req.Leader.Soul,
				Agents:            req.Leader.Agents,
				Package:           req.Leader.Package,
				Heartbeat:         toHeartbeatSpec(req.Leader.Heartbeat),
				WorkerIdleTimeout: req.Leader.WorkerIdleTimeout,
				ChannelPolicy:     req.Leader.ChannelPolicy,
				State:             req.Leader.State,
			},
		},
	}

	for _, tw := range req.Workers {
		team.Spec.Workers = append(team.Spec.Workers, v1beta1.TeamWorkerSpec{
			Name:          tw.Name,
			Model:         tw.Model,
			Runtime:       tw.Runtime,
			Image:         tw.Image,
			Identity:      tw.Identity,
			Soul:          tw.Soul,
			Agents:        tw.Agents,
			Skills:        tw.Skills,
			McpServers:    tw.McpServers,
			Package:       tw.Package,
			Expose:        tw.Expose,
			ChannelPolicy: tw.ChannelPolicy,
			State:         tw.State,
		})
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

	var team v1beta1.Team
	if err := h.client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &team); err != nil {
		writeK8sError(w, "get team for update", err)
		return
	}

	if req.Description != "" {
		team.Spec.Description = req.Description
	}
	if req.Admin != nil {
		team.Spec.Admin = req.Admin
	}
	if req.PeerMentions != nil {
		team.Spec.PeerMentions = req.PeerMentions
	}
	if req.ChannelPolicy != nil {
		team.Spec.ChannelPolicy = req.ChannelPolicy
	}
	if req.Leader != nil {
		if req.Leader.Model != "" {
			team.Spec.Leader.Model = req.Leader.Model
		}
		if req.Leader.Identity != "" {
			team.Spec.Leader.Identity = req.Leader.Identity
		}
		if req.Leader.Soul != "" {
			team.Spec.Leader.Soul = req.Leader.Soul
		}
		if req.Leader.Agents != "" {
			team.Spec.Leader.Agents = req.Leader.Agents
		}
		if req.Leader.Package != "" {
			team.Spec.Leader.Package = req.Leader.Package
		}
		if req.Leader.Heartbeat != nil {
			team.Spec.Leader.Heartbeat = toHeartbeatSpec(req.Leader.Heartbeat)
		}
		if req.Leader.WorkerIdleTimeout != "" {
			team.Spec.Leader.WorkerIdleTimeout = req.Leader.WorkerIdleTimeout
		}
		if req.Leader.ChannelPolicy != nil {
			team.Spec.Leader.ChannelPolicy = req.Leader.ChannelPolicy
		}
		if req.Leader.State != nil {
			team.Spec.Leader.State = req.Leader.State
		}
	}
	if req.Workers != nil {
		team.Spec.Workers = nil
		for _, tw := range req.Workers {
			team.Spec.Workers = append(team.Spec.Workers, v1beta1.TeamWorkerSpec{
				Name:          tw.Name,
				Model:         tw.Model,
				Runtime:       tw.Runtime,
				Image:         tw.Image,
				Identity:      tw.Identity,
				Soul:          tw.Soul,
				Agents:        tw.Agents,
				Skills:        tw.Skills,
				McpServers:    tw.McpServers,
				Package:       tw.Package,
				Expose:        tw.Expose,
				ChannelPolicy: tw.ChannelPolicy,
			})
		}
	}

	if err := h.client.Update(r.Context(), &team); err != nil {
		writeK8sError(w, "update team", err)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, teamToResponse(&team))
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
			DisplayName:       req.DisplayName,
			Email:             req.Email,
			PermissionLevel:   req.PermissionLevel,
			AccessibleTeams:   req.AccessibleTeams,
			AccessibleWorkers: req.AccessibleWorkers,
			Note:              req.Note,
		},
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
	}
	if resp.Phase == "" {
		resp.Phase = "Pending"
	}
	if w.Annotations != nil {
		resp.Team = w.Annotations["hiclaw.io/team"]
		resp.Role = w.Annotations["hiclaw.io/role"]
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
		LeaderName:        t.Spec.Leader.Name,
		LeaderHeartbeat:   t.Spec.Leader.Heartbeat,
		WorkerIdleTimeout: t.Spec.Leader.WorkerIdleTimeout,
		TeamRoomID:        t.Status.TeamRoomID,
		LeaderDMRoomID:    t.Status.LeaderDMRoomID,
		LeaderReady:       t.Status.LeaderReady,
		ReadyWorkers:      t.Status.ReadyWorkers,
		TotalWorkers:      t.Status.TotalWorkers,
		Message:           t.Status.Message,
	}
	if resp.Phase == "" {
		resp.Phase = "Pending"
	}
	for _, w := range t.Spec.Workers {
		resp.WorkerNames = append(resp.WorkerNames, w.Name)
	}
	if t.Status.WorkerExposedPorts != nil {
		resp.WorkerExposedPorts = make(map[string][]ExposedPortInfo)
		for wn, ports := range t.Status.WorkerExposedPorts {
			for _, p := range ports {
				resp.WorkerExposedPorts[wn] = append(resp.WorkerExposedPorts[wn], ExposedPortInfo{Port: p.Port, Domain: p.Domain})
			}
		}
	}
	return resp
}

func toHeartbeatSpec(req *TeamLeaderHeartbeatRequest) *v1beta1.TeamLeaderHeartbeatSpec {
	if req == nil {
		return nil
	}

	spec := &v1beta1.TeamLeaderHeartbeatSpec{
		Every: req.Every,
	}
	if req.Enabled != nil {
		spec.Enabled = *req.Enabled
	}
	if !spec.Enabled && spec.Every == "" {
		return nil
	}
	return spec
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
		MatrixUserID:    h.Status.MatrixUserID,
		InitialPassword: h.Status.InitialPassword,
		Rooms:           h.Status.Rooms,
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
