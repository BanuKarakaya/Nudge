package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"nudge/internal/coding"
	"strings"
)

func (h *DashboardHandler) WithBranchRules(store coding.BranchSettingsStore) { h.branchRules = store }
func (h *DashboardHandler) branchRulesAPI(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	if strings.TrimSpace(h.token) == "" {
		writeDashboardJSON(w, 503, map[string]string{"error": "Dashboard kapalı."})
		return
	}
	if !h.authorized(r) {
		writeDashboardJSON(w, 401, map[string]string{"error": "Dashboard erişim anahtarı gerekli."})
		return
	}
	if h.branchRules == nil {
		writeDashboardJSON(w, 503, map[string]string{"error": "Branch ayarları kullanılamıyor."})
		return
	}
	if r.Method == http.MethodGet {
		settings, err := h.branchRules.Settings(r.Context())
		if err != nil {
			writeDashboardJSON(w, 500, map[string]string{"error": "Kurallar yüklenemedi."})
			return
		}
		writeDashboardJSON(w, 200, settings)
		return
	}
	// Custom token header + JSON and cross-site rejection protect this admin mutation.
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeDashboardJSON(w, 403, map[string]string{"error": "Geçersiz kaynak."})
		return
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		writeDashboardJSON(w, 415, map[string]string{"error": "JSON gerekli."})
		return
	}
	var rule coding.BranchRule
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rule); err != nil {
		writeDashboardJSON(w, 400, map[string]string{"error": "Geçersiz ayar."})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeDashboardJSON(w, 400, map[string]string{"error": "Tek ayar gönderilmeli."})
		return
	}
	example := ""
	if rule.Template != "" || r.Method == http.MethodPost {
		var err error
		example, err = coding.ValidateBranchTemplate(rule.Template)
		if err != nil {
			writeDashboardJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
	}
	if r.Method == http.MethodPost {
		writeDashboardJSON(w, 200, map[string]string{"example": example})
		return
	}
	if err := h.branchRules.SaveRule(r.Context(), rule); err != nil {
		status := 500
		message := "Kural kaydedilemedi."
		if errors.Is(err, coding.ErrDenied) || errors.Is(err, coding.ErrArguments) {
			status = 400
			message = "Geçerli bir workspace ve o workspace’in kullanıcısını seç."
		}
		writeDashboardJSON(w, status, map[string]string{"error": message})
		return
	}
	writeDashboardJSON(w, 200, map[string]string{"example": example})
}
