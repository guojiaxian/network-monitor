package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAuditAndGuardAllowsOwnPasswordChange 改自己密码/退出登录属个人操作：观察者也可访问；
// 其余非 GET 请求观察者仍被 403
func TestAuditAndGuardAllowsOwnPasswordChange(t *testing.T) {
	a := &AdminAPI{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := a.auditAndGuard(next)

	call := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req = req.WithContext(context.WithValue(req.Context(),
			ctxUserKey, nwSession{user: "viewer1", role: "viewer"}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := call(http.MethodPut, "/api/admin/me/password"); rec.Code != http.StatusOK {
		t.Fatalf("观察者改自己密码应放行，实际=%d", rec.Code)
	}
	if rec := call(http.MethodPost, "/api/admin/logout"); rec.Code != http.StatusOK {
		t.Fatalf("观察者退出登录应放行（否则会话删不掉），实际=%d", rec.Code)
	}
	if rec := call(http.MethodPost, "/api/admin/users"); rec.Code != http.StatusForbidden {
		t.Fatalf("观察者其他写操作仍应 403，实际=%d", rec.Code)
	}
}
