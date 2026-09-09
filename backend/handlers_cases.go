package main

import (
	"log"
	"net/http"
)

// GET /api/cases — returns the live list of FIRs assigned to the requesting
// officer (badge_number), with each case's document title, classification and
// running version count. The list is assignment-driven (case_assignments), the
// same scope as the officer_own_assignments RLS policy: officers see their own
// cases, supervisors see the FIRs they were assigned to (the seed migration
// assigns the extra supervisors to every case), and SYSTEM_ADMIN — who holds no
// assignments — gets an empty list, consistently with the "admin never touches
// case content" rule.
func (a *app) handleCases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	badge := r.URL.Query().Get("badge_number")
	if sessionID == "" || !a.validSession(sessionID) {
		http.Error(w, "invalid or expired session", http.StatusUnauthorized)
		return
	}
	if badge == "" {
		http.Error(w, "badge_number is required", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	uid, err := a.st.userIDByBadge(ctx, badge)
	if err != nil {
		http.Error(w, "invalid badge", http.StatusUnauthorized)
		return
	}
	role, err := a.st.userRole(ctx, uid)
	if err != nil {
		log.Printf("cases role lookup error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	list, err := a.st.casesForOfficer(ctx, uid, role)
	if err != nil {
		log.Printf("cases list error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cases": list})
}