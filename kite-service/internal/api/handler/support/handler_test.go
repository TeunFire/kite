package support

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kitecloud/kite/kite-service/internal/api/handler"
	"github.com/kitecloud/kite/kite-service/internal/model"
	"github.com/kitecloud/kite/kite-service/internal/store"
	"gopkg.in/guregu/null.v4"
)

type testAppStore struct {
	store.AppStore
	requestedID string
}

func (s *testAppStore) App(_ context.Context, id string) (*model.App, error) {
	s.requestedID = id
	return &model.App{ID: id}, nil
}

type testEventListenerStore struct {
	store.EventListenerStore
	listeners []*model.EventListener
	listener  *model.EventListener
}

func (s *testEventListenerStore) EventListenersByApp(_ context.Context, _ string) ([]*model.EventListener, error) {
	return s.listeners, nil
}

func (s *testEventListenerStore) EventListener(_ context.Context, _ string) (*model.EventListener, error) {
	return s.listener, nil
}

type testCommandStore struct {
	store.CommandStore
	command *model.Command
}

func (s *testCommandStore) Command(_ context.Context, _ string) (*model.Command, error) {
	return s.command, nil
}

type testEditorSessionStore struct {
	store.EditorSessionStore
	session *store.EditorSession
	usage   []*store.EditorUsage
}

func (s *testEditorSessionStore) UpsertEditorSession(_ context.Context, session *store.EditorSession) error {
	s.session = session
	return nil
}

func (s *testEditorSessionStore) EditorUsageByApp(_ context.Context, _ string, _ time.Time) ([]*store.EditorUsage, error) {
	return s.usage, nil
}

func TestEventListenerListDoesNotExposeWebhookSecret(t *testing.T) {
	appStore := &testAppStore{}
	eventListenerStore := &testEventListenerStore{
		listeners: []*model.EventListener{{
			ID:            "listener-id",
			AppID:         "kite-app-id",
			Source:        model.EventSourceWebhook,
			WebhookSecret: null.StringFrom("private-webhook-secret"),
		}},
	}
	h := NewSupportHandler(appStore, nil, nil, eventListenerStore, nil)

	mux := http.NewServeMux()
	group := handler.Group(mux, "/v1/support/apps/{appID}")
	group.Get("/event-listeners", handler.Typed(h.HandleEventListenerList))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/support/apps/kite-app-id/event-listeners", nil))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", res.Code, http.StatusOK, res.Body.String())
	}
	if appStore.requestedID != "kite-app-id" {
		t.Fatalf("looked up app ID %q, want Kite App ID", appStore.requestedID)
	}
	if strings.Contains(res.Body.String(), "private-webhook-secret") || strings.Contains(res.Body.String(), "webhook_secret") {
		t.Fatalf("support response exposed a webhook secret: %s", res.Body.String())
	}
}

func TestEditorSessionUpsertUsesAuthenticatedAppAndUser(t *testing.T) {
	const sessionID = "550e8400-e29b-41d4-a716-446655440000"
	commandStore := &testCommandStore{command: &model.Command{
		ID:    "command-id",
		AppID: "kite-app-id",
	}}
	editorSessionStore := &testEditorSessionStore{}
	h := NewSupportHandler(nil, nil, commandStore, nil, editorSessionStore)

	mux := http.NewServeMux()
	group := handler.Group(
		mux,
		"/v1/apps/{appID}",
		func(next handler.HandlerFunc) handler.HandlerFunc {
			return func(c *handler.Context) error {
				c.App = &model.App{ID: "kite-app-id"}
				c.Session = &model.Session{UserID: "support-user"}
				return next(c)
			}
		},
	)
	group.Put("/editor-sessions/{sessionID}", handler.TypedWithBody(h.HandleEditorSessionUpsert))
	res := httptest.NewRecorder()
	body := `{"target_type":"command","target_id":"command-id","active_seconds":90,"save_count":2}`
	req := httptest.NewRequest(
		http.MethodPut,
		"/v1/apps/kite-app-id/editor-sessions/"+sessionID,
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", res.Code, http.StatusOK, res.Body.String())
	}
	if editorSessionStore.session == nil {
		t.Fatal("editor session was not saved")
	}
	if editorSessionStore.session.AppID != "kite-app-id" ||
		editorSessionStore.session.UserID != "support-user" ||
		editorSessionStore.session.ActiveSeconds != 90 ||
		editorSessionStore.session.SaveCount != 2 {
		t.Fatalf("saved unexpected editor session: %+v", editorSessionStore.session)
	}
}

func TestEditorUsageGetAggregatesTotals(t *testing.T) {
	lastEditedAt := time.Date(2026, time.November, 12, 12, 0, 0, 0, time.UTC)
	appStore := &testAppStore{}
	editorSessionStore := &testEditorSessionStore{
		usage: []*store.EditorUsage{
			{
				TargetType:    "command",
				TargetID:      "command-id",
				ActiveSeconds: 120,
				SaveCount:     3,
				SessionCount:  2,
				LastEditedAt:  &lastEditedAt,
			},
			{
				TargetType:    "event_listener",
				TargetID:      "listener-id",
				ActiveSeconds: 60,
				SaveCount:     1,
				SessionCount:  1,
			},
		},
	}
	h := NewSupportHandler(appStore, nil, nil, nil, editorSessionStore)

	mux := http.NewServeMux()
	group := handler.Group(mux, "/v1/support/apps/{appID}")
	group.Get("/editor-usage", handler.Typed(h.HandleEditorUsageGet))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/support/apps/kite-app-id/editor-usage", nil))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", res.Code, http.StatusOK, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"active_seconds":180`) ||
		!strings.Contains(res.Body.String(), `"save_count":4`) ||
		!strings.Contains(res.Body.String(), `"session_count":3`) {
		t.Fatalf("response did not include aggregated editor usage: %s", res.Body.String())
	}
}
