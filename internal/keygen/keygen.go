// Package keygen produces paste keys with three strategies (random, phonetic,
// dictionary). It uses crypto/rand rather than math/rand so keys are not
// predictable.
package keygen

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"strings"
)

// Generator creates a paste key of the requested length.
type Generator interface {
	CreateKey(length int) string
}

// randInt returns a uniformly random int in [0, n) using crypto/rand.
// n must be > 0.
func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		// crypto/rand failures are catastrophic and effectively never happen;
		// panicking is preferable to emitting a weak/zero key.
		panic("keygen: crypto/rand failure: " + err.Error())
	}
	return int(v.Int64())
}

// Random selects characters uniformly from a fixed keyspace.
type Random struct {
	keyspace []rune
}

const defaultKeyspace = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// NewRandom returns a Random generator. An empty keyspace falls back to the
// default alphabet.
func NewRandom(keyspace string) *Random {
	if keyspace == "" {
		keyspace = defaultKeyspace
	}
	return &Random{keyspace: []rune(keyspace)}
}

func (g *Random) CreateKey(length int) string {
	var b strings.Builder
	for i := 0; i < length; i++ {
		b.WriteRune(g.keyspace[randInt(len(g.keyspace))])
	}
	return b.String()
}

// Phonetic alternates consonants and vowels for pronounceable keys, with a
// randomized start offset.
type Phonetic struct{}

const (
	vowels     = "aeiou"
	consonants = "bcdfghjklmnpqrstvwxyz"
)

func NewPhonetic() *Phonetic { return &Phonetic{} }

func (g *Phonetic) CreateKey(length int) string {
	var b strings.Builder
	start := randInt(2) // 0 or 1: whether even-index chars are consonants or vowels
	for i := 0; i < length; i++ {
		if i%2 == start {
			b.WriteByte(consonants[randInt(len(consonants))])
		} else {
			b.WriteByte(vowels[randInt(len(vowels))])
		}
	}
	return b.String()
}

// Dictionary concatenates `length` words drawn from a word list.
type Dictionary struct {
	words []string
}

// NewDictionary loads a newline-separated word list from path.
func NewDictionary(path string) (*Dictionary, error) {
	if path == "" {
		return nil, fmt.Errorf("dictionary generator requires a word list path")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open dictionary %q: %w", path, err)
	}
	defer f.Close()

	var words []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if w := strings.TrimSpace(sc.Text()); w != "" {
			words = append(words, w)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read dictionary %q: %w", path, err)
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("dictionary %q is empty", path)
	}
	return &Dictionary{words: words}, nil
}

func (g *Dictionary) CreateKey(length int) string {
	var b strings.Builder
	for i := 0; i < length; i++ {
		b.WriteString(g.words[randInt(len(g.words))])
	}
	return b.String()
}

// New builds a generator from a type name. Recognized: "random", "phonetic",
// "dictionary". An unknown or empty type defaults to phonetic. dictPath is
// required only for the dictionary type.
func New(genType, dictPath string) (Generator, error) {
	switch genType {
	case "random":
		return NewRandom(""), nil
	case "dictionary":
		return NewDictionary(dictPath)
	case "phonetic", "":
		return NewPhonetic(), nil
	default:
		return nil, fmt.Errorf("unknown key generator type %q", genType)
	}
}
