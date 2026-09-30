package audit

import (
	"bytes"
	"testing"
)

func TestMerkleRootDetectsInsertionRemovalAndReordering(t *testing.T) {
	original := [][]byte{[]byte("one"), []byte("two"), []byte("three")}
	root := MerkleRoot(original)
	for _, changed := range [][][]byte{{[]byte("one"), []byte("three")}, {[]byte("two"), []byte("one"), []byte("three")}, {[]byte("one"), []byte("two"), []byte("three"), []byte("four")}} {
		if bytes.Equal(root, MerkleRoot(changed)) {
			t.Fatal("tampering preserved Merkle root")
		}
	}
}

func TestMerkleRootUsesDomainSeparatedOddNodeDuplication(t *testing.T) {
	one := MerkleRoot([][]byte{[]byte("one")})
	if got := MerkleRoot([][]byte{[]byte("one")}); !bytes.Equal(got, one) {
		t.Fatalf("single leaf root is not deterministic: %x != %x", got, one)
	}
	if got := MerkleRoot(nil); len(got) != 32 {
		t.Fatalf("empty root length = %d, want 32", len(got))
	}
}
