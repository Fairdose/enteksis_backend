package httpapi

import "testing"

func TestPasswordHashVerification(t *testing.T) {
	hash, err := passwordHash("güçlü ve uzun bir parola")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword("güçlü ve uzun bir parola", hash) {
		t.Fatal("doğru parola reddedildi")
	}
	if verifyPassword("yanlış parola", hash) {
		t.Fatal("yanlış parola kabul edildi")
	}
	if verifyPassword("güçlü ve uzun bir parola", "bozuk-hash") {
		t.Fatal("bozuk hash kabul edildi")
	}
}
