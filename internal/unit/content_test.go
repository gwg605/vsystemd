package unit

import "testing"

func TestContentHash(t *testing.T) {
	// sha256("") =
	// e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
	if got := Content(nil).Hash(); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("empty Hash = %q", got)
	}

	// sha256("abc") =
	// ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad
	if got := Content("abc").Hash(); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("abc Hash = %q", got)
	}
}
