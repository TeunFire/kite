package support

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kitecloud/kite/kite-service/internal/api/handler"
	"github.com/kitecloud/kite/kite-service/internal/api/wire"
	"github.com/kitecloud/kite/kite-service/internal/model"
	"github.com/kitecloud/kite/kite-service/internal/store"
	"github.com/kitecloud/kite/kite-service/pkg/flow"
	"gopkg.in/guregu/null.v4"
)

var editorSessionIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type SupportHandler struct {
	appStore           store.AppStore
	logStore           store.LogStore
	commandStore       store.CommandStore
	eventListenerStore store.EventListenerStore
	editorSessionStore store.EditorSessionStore
}

func NewSupportHandler(
	appStore store.AppStore,
	logStore store.LogStore,
	commandStore store.CommandStore,
	eventListenerStore store.EventListenerStore,
	editorSessionStore store.EditorSessionStore,
) *SupportHandler {
	return &SupportHandler{
		appStore:           appStore,
		logStore:           logStore,
		commandStore:       commandStore,
		eventListenerStore: eventListenerStore,
		editorSessionStore: editorSessionStore,
	}
}

type AccessResponse struct {
	Authorized bool `json:"authorized"`
}

type EventListener struct {
	ID              string                    `json:"id"`
	Source          string                    `json:"source"`
	Type            string                    `json:"type"`
	Description     string                    `json:"description"`
	Enabled         bool                      `json:"enabled"`
	AppID           string                    `json:"app_id"`
	BundleInstallID null.String               `json:"bundle_install_id"`
	CreatorUserID   string                    `json:"creator_user_id"`
	Filter          *wire.EventListenerFilter `json:"filter"`
	FlowSource      flow.FlowData             `json:"flow_source"`
	CreatedAt       time.Time                 `json:"created_at"`
	UpdatedAt       time.Time                 `json:"updated_at"`
	LastRunAt       null.Time                 `json:"last_run_at"`
}

type EventListenerListResponse []*EventListener

type LogEntryListResponse struct {
	Entries    []*wire.LogEntry `json:"entries"`
	NextBefore int64            `json:"next_before"`
}

type EditorSessionUpsertRequest struct {
	TargetType    string `json:"target_type"`
	TargetID      string `json:"target_id"`
	ActiveSeconds int    `json:"active_seconds"`
	SaveCount     int    `json:"save_count"`
}

type EditorSessionUpsertResponse struct {
	Saved bool `json:"saved"`
}

type EditorUsageTotals struct {
	ActiveSeconds int64      `json:"active_seconds"`
	SaveCount     int64      `json:"save_count"`
	SessionCount  int64      `json:"session_count"`
	LastEditedAt  *time.Time `json:"last_edited_at"`
}

type EditorUsageResponse struct {
	Total EditorUsageTotals    `json:"total"`
	Items []*store.EditorUsage `json:"items"`
}

func (h *SupportHandler) HandleAccessGet(c *handler.Context) (*AccessResponse, error) {
	return &AccessResponse{Authorized: true}, nil
}

func (h *SupportHandler) HandleAppGet(c *handler.Context) (*wire.AppGetResponse, error) {
	app, err := h.app(c)
	if err != nil {
		return nil, err
	}
	return wire.AppToWire(app), nil
}

func (h *SupportHandler) HandleLogEntryList(c *handler.Context) (*LogEntryListResponse, error) {
	beforeID := int64(0)
	if raw := c.Query("before"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			return nil, handler.ErrBadRequest("invalid_before", "before must be a non-negative log ID")
		}
		beforeID = parsed
	}

	limit := 100
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return nil, handler.ErrBadRequest("invalid_limit", "limit must be between 1 and 100")
		}
		limit = parsed
	}

	app, err := h.app(c)
	if err != nil {
		return nil, err
	}
	entries, err := h.logStore.LogEntriesByApp(c.Context(), app.ID, beforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get support log entries: %w", err)
	}

	levelFilter := c.Query("level")
	messageFilter := strings.ToLower(c.Query("query"))
	res := &LogEntryListResponse{Entries: make([]*wire.LogEntry, 0, len(entries))}
	for _, entry := range entries {
		if levelFilter != "" && !strings.EqualFold(string(entry.Level), levelFilter) {
			continue
		}
		if messageFilter != "" && !strings.Contains(strings.ToLower(entry.Message), messageFilter) {
			continue
		}
		res.Entries = append(res.Entries, wire.LogEntryToWire(entry))
	}

	if len(entries) == limit {
		res.NextBefore = entries[len(entries)-1].ID
	}
	return res, nil
}

func (h *SupportHandler) HandleCommandList(c *handler.Context) (*wire.CommandListResponse, error) {
	app, err := h.app(c)
	if err != nil {
		return nil, err
	}
	commands, err := h.commandStore.CommandsByApp(c.Context(), app.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get support commands: %w", err)
	}
	res := make([]*wire.Command, len(commands))
	for i, command := range commands {
		res[i] = wire.CommandToWire(command)
	}
	return &res, nil
}

func (h *SupportHandler) HandleEventListenerList(c *handler.Context) (*EventListenerListResponse, error) {
	app, err := h.app(c)
	if err != nil {
		return nil, err
	}
	listeners, err := h.eventListenerStore.EventListenersByApp(c.Context(), app.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get support event listeners: %w", err)
	}
	res := make(EventListenerListResponse, len(listeners))
	for i, listener := range listeners {
		res[i] = &EventListener{
			ID:              listener.ID,
			Source:          string(listener.Source),
			Type:            string(listener.Type),
			Description:     listener.Description,
			Enabled:         listener.Enabled,
			AppID:           listener.AppID,
			BundleInstallID: listener.BundleInstallID,
			CreatorUserID:   listener.CreatorUserID,
			Filter:          (*wire.EventListenerFilter)(listener.Filter),
			FlowSource:      listener.FlowSource,
			CreatedAt:       listener.CreatedAt,
			UpdatedAt:       listener.UpdatedAt,
			LastRunAt:       listener.LastRunAt,
		}
	}
	return &res, nil
}

func (h *SupportHandler) HandleEditorSessionUpsert(
	c *handler.Context,
	req EditorSessionUpsertRequest,
) (*EditorSessionUpsertResponse, error) {
	sessionID := c.Param("sessionID")
	if !editorSessionIDPattern.MatchString(sessionID) {
		return nil, handler.ErrBadRequest("invalid_editor_session", "session ID must be a UUID")
	}
	if req.TargetID == "" || len(req.TargetID) > 256 {
		return nil, handler.ErrBadRequest("invalid_editor_target", "target ID is required")
	}
	if req.ActiveSeconds < 0 || req.ActiveSeconds > int((30*24*time.Hour).Seconds()) {
		return nil, handler.ErrBadRequest("invalid_active_seconds", "active seconds must be between 0 and 30 days")
	}
	if req.SaveCount < 0 || req.SaveCount > 1_000_000 {
		return nil, handler.ErrBadRequest("invalid_save_count", "save count must be between 0 and 1000000")
	}

	if c.App == nil || c.Session == nil {
		return nil, fmt.Errorf("editor session request missing app or user context")
	}
	if err := h.validateEditorTarget(c, req.TargetType, req.TargetID); err != nil {
		return nil, err
	}

	err := h.editorSessionStore.UpsertEditorSession(c.Context(), &store.EditorSession{
		ID:            sessionID,
		AppID:         c.App.ID,
		TargetType:    req.TargetType,
		TargetID:      req.TargetID,
		UserID:        c.Session.UserID,
		ActiveSeconds: req.ActiveSeconds,
		SaveCount:     req.SaveCount,
	})
	if err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return nil, handler.ErrConflict("editor_session_conflict", "editor session does not match its original target")
		}
		return nil, fmt.Errorf("failed to record editor session: %w", err)
	}
	return &EditorSessionUpsertResponse{Saved: true}, nil
}

func (h *SupportHandler) HandleEditorUsageGet(c *handler.Context) (*EditorUsageResponse, error) {
	app, err := h.app(c)
	if err != nil {
		return nil, err
	}
	since := time.Now().UTC().Add(-30 * 24 * time.Hour)
	items, err := h.editorSessionStore.EditorUsageByApp(c.Context(), app.ID, since)
	if err != nil {
		return nil, fmt.Errorf("failed to get support editor usage: %w", err)
	}

	res := &EditorUsageResponse{Items: items}
	for _, item := range items {
		res.Total.ActiveSeconds += item.ActiveSeconds
		res.Total.SaveCount += item.SaveCount
		res.Total.SessionCount += item.SessionCount
		if item.LastEditedAt != nil &&
			(res.Total.LastEditedAt == nil || item.LastEditedAt.After(*res.Total.LastEditedAt)) {
			lastEditedAt := *item.LastEditedAt
			res.Total.LastEditedAt = &lastEditedAt
		}
	}
	return res, nil
}

func (h *SupportHandler) validateEditorTarget(c *handler.Context, targetType string, targetID string) error {
	switch targetType {
	case "command":
		command, err := h.commandStore.Command(c.Context(), targetID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return handler.ErrNotFound("unknown_command", "Command not found")
			}
			return fmt.Errorf("failed to verify editor command: %w", err)
		}
		if command.AppID != c.App.ID {
			return handler.ErrNotFound("unknown_command", "Command not found")
		}
	case "event_listener":
		listener, err := h.eventListenerStore.EventListener(c.Context(), targetID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return handler.ErrNotFound("unknown_event_listener", "Event listener not found")
			}
			return fmt.Errorf("failed to verify editor event listener: %w", err)
		}
		if listener.AppID != c.App.ID {
			return handler.ErrNotFound("unknown_event_listener", "Event listener not found")
		}
	default:
		return handler.ErrBadRequest("invalid_editor_target_type", "target type must be command or event_listener")
	}
	return nil
}

func (h *SupportHandler) app(c *handler.Context) (*model.App, error) {
	appID := c.Param("appID")
	if appID == "" {
		return nil, handler.ErrBadRequest("missing_app_id", "Kite App ID is required")
	}
	app, err := h.appStore.App(c.Context(), appID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, handler.ErrNotFound("unknown_app", "App not found")
		}
		return nil, fmt.Errorf("failed to get support app: %w", err)
	}
	return app, nil
}
