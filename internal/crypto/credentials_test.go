package crypto

import "testing"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c, err := Encrypt("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", "123", Credentials{"u", "p", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decrypt("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", "123", c)
	if err != nil {
		t.Fatal(err)
	}
	if out.UADEUsername != "u" || out.UADEPassword != "p" || out.UADEStartURL != "https://x" {
		t.Fatalf("unexpected %+v", out)
	}
}

func TestDecryptRejectsWrongUser(t *testing.T) {
	c, _ := Encrypt("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", "123", Credentials{"u", "p", ""})
	if _, err := Decrypt("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", "999", c); err == nil {
		t.Fatal("expected auth failure")
	}
}
