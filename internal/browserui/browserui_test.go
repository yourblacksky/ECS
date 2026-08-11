package browserui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesBrowserLeafWithSecurityHeaders(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/browser/", nil)
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "ECS Browser Leaf") {
		t.Fatal("browser leaf page was not served")
	}
	if got := response.Header().Get("Content-Security-Policy"); !strings.Contains(got, "connect-src") {
		t.Fatalf("missing content security policy: %q", got)
	}
}

func TestBrowserLeafReconnectButtonIsEnabledAfterAuthentication(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/browser/app.js", nil)
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	script := response.Body.String()
	authenticated := strings.Index(script, `message.type === "authenticated"`)
	enableButton := strings.Index(script, `$("#connectButton").disabled = false;`)
	if authenticated < 0 || enableButton < authenticated {
		t.Fatal("authenticated browser node does not re-enable reconnect button")
	}
}

func TestBrowserLeafHandlesTaskCancellation(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/browser/app.js", nil)
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	script := response.Body.String()
	for _, expected := range []string{
		`message.type === "task_cancel"`,
		`state.canceledTasks.add(message.task_id)`,
		`state.canceledTasks.delete(message.task_id)`,
		`任务已被上级取消`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("browser cancellation handling missing %q", expected)
		}
	}
}
