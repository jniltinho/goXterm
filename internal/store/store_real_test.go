package store

import (
	"os"
	"testing"
)

// TestRealStore reads a real rustXterm store (set RXT_STORE_DIR and RXT_MASTER_PASSWORD) and checks
// that sessions, saved credentials and trust records load. Skipped without those env vars, so the
// normal test run never touches a user's store. Never prints secret bytes.
func TestRealStore(t *testing.T) {
	dir, pw := os.Getenv("RXT_STORE_DIR"), os.Getenv("RXT_MASTER_PASSWORD")
	if dir == "" || pw == "" {
		t.Skip("set RXT_STORE_DIR and RXT_MASTER_PASSWORD to run against a real store")
	}
	st, err := Open(dir, pw)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	sessions, err := st.Sessions()
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("no sessions read")
	}
	folders, _ := st.Folders()
	creds, _ := st.SavedCredentialCount()
	sshTrust, _ := st.SSHTrustCount()
	certTrust, _ := st.CertTrustCount()
	known, _ := KnownHostsCount()
	t.Logf("sessions=%d folders=%d credentials=%d ssh_trust=%d cert_trust=%d known_hosts=%d",
		len(sessions), len(folders), creds, sshTrust, certTrust, known)

	// If any session has a saved password, confirm it decrypts to non-empty bytes (never shown).
	for _, s := range sessions {
		if s["password"] != "saved" {
			continue
		}
		id := s["id"].(int64)
		secret, ok, err := st.Credential(id, CredPassword)
		if err != nil || !ok {
			t.Fatalf("credential read for session %d: ok=%v err=%v", id, ok, err)
		}
		if len(secret) == 0 {
			t.Fatalf("credential for session %d is empty", id)
		}
		t.Logf("credential for session %d read ok (%d bytes, not shown)", id, len(secret))
		break
	}
}
