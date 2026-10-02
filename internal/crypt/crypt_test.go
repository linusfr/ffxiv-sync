package crypt

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	plain := []byte(`{"PushoverToken":"secret"}`)

	sealed, err := Seal("hunter2", plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("secret")) {
		t.Fatal("the plaintext is still in there")
	}
	if !Encrypted(sealed) {
		t.Error("a sealed blob is not recognised as one")
	}

	opened, err := Open("hunter2", sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plain) {
		t.Errorf("got %q, want %q", opened, plain)
	}
}

func TestWrongPassphraseFails(t *testing.T) {
	sealed, err := Seal("right", []byte("settings"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Open("wrong", sealed); err == nil {
		t.Fatal("a wrong passphrase was accepted")
	}
	if _, err := Open("", sealed); err != ErrNoPassphrase {
		t.Errorf("no passphrase gave %v, want ErrNoPassphrase", err)
	}
}

// A store written before encryption was turned on still reads.
func TestPlainBlobsPassThrough(t *testing.T) {
	plain := []byte("not sealed")

	opened, err := Open("hunter2", plain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plain) {
		t.Error("a plain blob was mangled")
	}
}
