package keygen

import (
	"strings"
	"testing"
)

func TestRandomKeyLengthAndAlphabet(t *testing.T) {
	g := NewRandom("")
	for _, n := range []int{1, 10, 32} {
		k := g.CreateKey(n)
		if len([]rune(k)) != n {
			t.Fatalf("length = %d, want %d", len([]rune(k)), n)
		}
		for _, r := range k {
			if !strings.ContainsRune(defaultKeyspace, r) {
				t.Fatalf("rune %q not in keyspace", r)
			}
		}
	}
}

func TestPhoneticAlternatesAndLength(t *testing.T) {
	g := NewPhonetic()
	k := g.CreateKey(10)
	if len(k) != 10 {
		t.Fatalf("length = %d, want 10", len(k))
	}
	for _, r := range k {
		if !strings.ContainsRune(vowels, r) && !strings.ContainsRune(consonants, r) {
			t.Fatalf("rune %q neither vowel nor consonant", r)
		}
	}
}

func TestRandomReasonablyUnique(t *testing.T) {
	g := NewRandom("")
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		k := g.CreateKey(10)
		if seen[k] {
			t.Fatalf("duplicate key within 1000 draws: %q", k)
		}
		seen[k] = true
	}
}

func TestNewDefaultsToPhonetic(t *testing.T) {
	g, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.(*Phonetic); !ok {
		t.Fatalf("default generator = %T, want *Phonetic", g)
	}
}

func TestNewUnknownType(t *testing.T) {
	if _, err := New("bogus", ""); err == nil {
		t.Fatal("expected error for unknown generator type")
	}
}
