package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	configv2 "paw/internal/config"
)

// modelOption 是模型选择器的一项：ID 为目录标识（provider/name），
// ReasoningCapable 决定推理强度选择器是否可用，Effort 为当前生效的强度。
type modelOption struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Provider         string `json:"provider"`
	Source           string `json:"source"`
	ReasoningCapable bool   `json:"reasoning_capable"`
	Effort           string `json:"effort,omitempty"`
}

// modelOptionsResponse 描述输入框上方「卡片堆」选择器所需的全部状态。
type modelOptionsResponse struct {
	ActiveModelID string        `json:"active_model_id"`
	Models        []modelOption `json:"models"`
	EffortOptions []string      `json:"effort_options"`
}

// effortChoices 是推理强度的可选档位；"default" 表示不显式设置，
// 写回配置时移除 parameters.reasoning.effort。
var effortChoices = []string{"default", "low", "medium", "high", "max"}

// modelEffort 读取模型生效参数中的推理强度；未设置返回 ""。
func modelEffort(model configv2.Model) string {
	reasoning, ok := model.Parameters["reasoning"].(map[string]any)
	if !ok {
		return ""
	}
	effort, _ := reasoning["effort"].(string)
	return strings.ToLower(strings.TrimSpace(effort))
}

func isValidEffort(effort string) bool {
	for _, choice := range effortChoices {
		if effort == choice {
			return true
		}
	}
	return false
}

func modelOptionsFromSnapshot(snapshot configv2.Snapshot) modelOptionsResponse {
	models := make([]modelOption, 0, len(snapshot.EffectiveModels))
	for id, item := range snapshot.EffectiveModels {
		models = append(models, modelOption{
			ID:               id,
			Name:             item.Model.Name,
			Provider:         item.Model.Provider,
			Source:           string(item.Source),
			ReasoningCapable: item.Model.Capabilities.Reasoning != nil && *item.Model.Capabilities.Reasoning,
			Effort:           modelEffort(item.Model),
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return modelOptionsResponse{ActiveModelID: snapshot.ActiveModelID, Models: models, EffortOptions: effortChoices}
}

// handleModelOptions 返回当前工作区可切换的模型目录与推理强度选项。
func (s *Server) handleModelOptions(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime(request)
	if !ok {
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", "workspace is not loaded", RequestID(request.Context()))
		return
	}
	if runtime.ConfigController == nil {
		writeJSONError(writer, http.StatusServiceUnavailable, "config_unavailable", "config controller is not available", RequestID(request.Context()))
		return
	}
	writeJSON(writer, http.StatusOK, modelOptionsFromSnapshot(runtime.ConfigController.Snapshot()))
}

// modelSelectRequest 是切换请求体：ModelID 切换模型；Effort 非空时把目标
// 模型（默认当前激活模型）的 parameters.reasoning.effort 调整为该档位，
// "default" 表示移除显式设置。
type modelSelectRequest struct {
	ModelID string `json:"model_id,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// handleModelSelect 切换激活模型和/或调整推理强度，写回全局配置并热应用到
// 模型运行时（下一个 turn 生效）。
func (s *Server) handleModelSelect(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime(request)
	if !ok {
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", "workspace is not loaded", RequestID(request.Context()))
		return
	}
	controller := runtime.ConfigController
	if controller == nil {
		writeJSONError(writer, http.StatusServiceUnavailable, "config_unavailable", "config controller is not available", RequestID(request.Context()))
		return
	}
	var body modelSelectRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", RequestID(request.Context()))
		return
	}
	body.ModelID = strings.TrimSpace(body.ModelID)
	body.Effort = strings.ToLower(strings.TrimSpace(body.Effort))
	if body.ModelID == "" && body.Effort == "" {
		writeJSONError(writer, http.StatusBadRequest, "empty_selection", "model_id or effort is required", RequestID(request.Context()))
		return
	}
	if body.Effort != "" && !isValidEffort(body.Effort) {
		writeJSONError(writer, http.StatusBadRequest, "invalid_effort", "effort must be one of "+strings.Join(effortChoices, ", "), RequestID(request.Context()))
		return
	}

	if body.ModelID != "" {
		if err := controller.SetActiveModelID(body.ModelID); err != nil {
			writeJSONError(writer, http.StatusBadRequest, "activate_model_failed", err.Error(), RequestID(request.Context()))
			return
		}
	}
	if body.Effort != "" {
		if err := applyEffort(request, controller, body.ModelID, body.Effort); err != nil {
			writeJSONError(writer, http.StatusBadRequest, "apply_effort_failed", err.Error(), RequestID(request.Context()))
			return
		}
	}
	writeJSON(writer, http.StatusOK, modelOptionsFromSnapshot(controller.Snapshot()))
}

// applyEffort 将目标模型的 parameters.reasoning.effort 写入全局配置。
// 目标模型缺省为当前激活模型；乐观并发冲突时基于最新快照重试一次。
func applyEffort(request *http.Request, controller *configv2.Controller, modelID, effort string) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		snapshot := controller.Snapshot()
		targetID := modelID
		if targetID == "" {
			targetID = snapshot.ActiveModelID
		}
		item, ok := snapshot.EffectiveModels[targetID]
		if !ok {
			return errModelNotFound(targetID)
		}
		updated := item.Model
		parameters := make(map[string]any, len(updated.Parameters)+1)
		for key, value := range updated.Parameters {
			parameters[key] = value
		}
		if effort == "default" {
			if reasoning, ok := parameters["reasoning"].(map[string]any); ok {
				next := make(map[string]any, len(reasoning))
				for key, value := range reasoning {
					if key != "effort" {
						next[key] = value
					}
				}
				if len(next) == 0 {
					delete(parameters, "reasoning")
				} else {
					parameters["reasoning"] = next
				}
			}
		} else {
			reasoning, _ := parameters["reasoning"].(map[string]any)
			next := make(map[string]any, len(reasoning)+1)
			for key, value := range reasoning {
				next[key] = value
			}
			next["effort"] = effort
			parameters["reasoning"] = next
		}
		updated.Parameters = parameters
		if _, lastErr = controller.UpdateConfig(request.Context(), snapshot.Revision, []configv2.Operation{configv2.UpsertModel(targetID, updated)}); lastErr == nil {
			return nil
		}
	}
	return lastErr
}

type errModelNotFound string

func (e errModelNotFound) Error() string {
	return "model " + string(e) + " is not in the effective catalog"
}
