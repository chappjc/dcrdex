package order

import (
	"bytes"
	crand "crypto/rand"
	"fmt"
	"math/rand"
	"testing"

	"decred.org/dcrdex/dex/msgjson"
	"decred.org/dcrdex/dex/order"
	"github.com/decred/dcrd/crypto/blake256"
)

func makeEpochOrderNote(mid string, side uint8, oid order.OrderID, rate uint64,
	qty uint64, commitment order.Commitment) *msgjson.EpochOrderNote {
	return &msgjson.EpochOrderNote{
		BookOrderNote: msgjson.BookOrderNote{
			TradeNote: msgjson.TradeNote{
				Side:     side,
				Rate:     rate,
				Quantity: qty,
			},
			OrderNote: msgjson.OrderNote{
				MarketID:   mid,
				OrderID:    oid[:],
				Commitment: commitment[:],
			},
		},
	}
}

func makeCommitment(pimg order.Preimage) order.Commitment {
	return order.Commitment(blake256.Sum256(pimg[:]))
}

// makeMatchProof generates the sorting seed and commitment checksum from the
// provided ordered set of preimages and commitments.
func makeMatchProof(preimages []order.Preimage, commitments []order.Commitment) ([]byte, []byte, error) {
	if len(preimages) != len(commitments) {
		return nil, nil, fmt.Errorf("expected equal number of preimages and commitments")
	}

	sbuff := make([]byte, 0, len(preimages)*order.PreimageSize)
	cbuff := make([]byte, 0, len(commitments)*order.CommitmentSize)
	for i := 0; i < len(preimages); i++ {
		sbuff = append(sbuff, preimages[i][:]...)
		cbuff = append(cbuff, commitments[i][:]...)
	}
	seed := blake256.Sum256(sbuff)
	csum := blake256.Sum256(cbuff)
	return seed[:], csum[:], nil
}

func randOrderID() (oid order.OrderID) {
	crand.Read(oid[:])
	return
}

func randPreimage() (pi order.Preimage) {
	crand.Read(pi[:])
	return
}

func BenchmarkEpochQueue(b *testing.B) {
	sz := 500
	notes := make([]*msgjson.EpochOrderNote, sz)
	preimages := make([]order.Preimage, 0, sz)
	for i := range notes {
		pi := randPreimage()
		notes[i] = makeEpochOrderNote("mkt", msgjson.BuyOrderNum, randOrderID(), 1, 3, blake256.Sum256(pi[:]))
		preimages = append(preimages, pi)
	}

	numMisses := sz / 20
	misses := make([]order.OrderID, numMisses)
	for i := range misses {
		in := rand.Intn(len(notes))
		copy(misses[i][:], notes[in].OrderID)
	}

	eq := NewEpochQueue()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		eq.Reset()

		for _, note := range notes {
			eq.Enqueue(note)
		}

		_, _, err := eq.GenerateMatchProof(preimages, misses)
		if err != nil {
			b.Error(err)
		}
	}
}

func TestEpochQueue(t *testing.T) {
	mid := "mkt"
	eq := NewEpochQueue()
	n1Pimg := [32]byte{'1'}
	n1Commitment := makeCommitment(n1Pimg)
	n1OrderID := [32]byte{'a'}
	n1 := makeEpochOrderNote(mid, msgjson.BuyOrderNum, n1OrderID, 1, 3, n1Commitment)
	eq.Enqueue(n1)

	// Ensure the epoch queue size is 1.
	if eq.Size() != 1 {
		t.Fatalf("[Size] expected queue size of %d, got %d", 1, eq.Size())
	}

	// Reset the epoch queue.
	eq.Reset()

	// Ensure the epoch queue size is 0.
	if eq.Size() != 0 {
		t.Fatalf("[Size] expected queue size of %d, got %d", 0, eq.Size())
	}

	eq.Enqueue(n1)

	n2Pimg := [32]byte{'2'}
	n2Commitment := makeCommitment(n2Pimg)
	n2OrderID := [32]byte{'b'}
	n2 := makeEpochOrderNote(mid, msgjson.BuyOrderNum, n2OrderID, 2, 4, n2Commitment)
	eq.Enqueue(n2)

	n3Pimg := [32]byte{'3'}
	n3Commitment := makeCommitment(n3Pimg)
	n3OrderID := [32]byte{'c'}
	n3 := makeEpochOrderNote(mid, msgjson.BuyOrderNum, n3OrderID, 3, 6, n3Commitment)
	eq.Enqueue(n3)

	// Ensure the queue has n2 epoch order.
	if !eq.Exists(n2OrderID) {
		t.Fatalf("[Exists] expected order with id %x in the epoch queue", n2OrderID)
	}

	// Ensure the epoch queue size is 3.
	if eq.Size() != 3 {
		t.Fatalf("[Size] expected queue size of %d, got %d", 3, eq.Size())
	}

	// Ensure match proof generation works as expected.
	preimages := []order.Preimage{n1Pimg, n2Pimg, n3Pimg}
	commitments := []order.Commitment{n3Commitment, n1Commitment, n2Commitment}
	expectedSeed, expectedCmtChecksum, err := makeMatchProof(preimages, commitments)
	if err != nil {
		t.Fatalf("[makeMatchProof] unexpected error: %v", err)
	}

	seed, cmtChecksum, err := eq.GenerateMatchProof(preimages, nil)
	if err != nil {
		t.Fatalf("[GenerateMatchProof] unexpected error: %v", err)
	}

	if !bytes.Equal(expectedSeed, seed) {
		t.Fatalf("expected seed %x, got %x", expectedSeed, seed)
	}

	if !bytes.Equal(expectedCmtChecksum, cmtChecksum) {
		t.Fatalf("expected commitment checksum %x, got %x",
			expectedCmtChecksum, cmtChecksum)
	}

	eq.Reset()

	// Queue epoch orders.
	eq.Enqueue(n3)
	eq.Enqueue(n1)
	eq.Enqueue(n2)

	// Ensure the queue has n1 epoch order.
	if !eq.Exists(n1OrderID) {
		t.Fatalf("[Exists] expected order with id %x in the epoch queue", n1OrderID)
	}

	// Ensure match proof generation works as expected, when there are misses.
	preimages = []order.Preimage{n1Pimg, n3Pimg}
	commitments = []order.Commitment{n3Commitment, n1Commitment}
	expectedSeed, expectedCmtChecksum, err = makeMatchProof(preimages, commitments)
	if err != nil {
		t.Fatalf("[makeMatchProof] unexpected error: %v", err)
	}

	var oidn2 order.OrderID
	copy(oidn2[:], n2.OrderID)
	misses := []order.OrderID{oidn2}
	seed, cmtChecksum, err = eq.GenerateMatchProof(preimages, misses)
	if err != nil {
		t.Fatalf("[GenerateMatchProof] unexpected error: %v", err)
	}

	if !bytes.Equal(expectedSeed, seed) {
		t.Fatalf("expected seed %x, got %x", expectedSeed, seed)
	}

	if !bytes.Equal(expectedCmtChecksum, cmtChecksum) {
		t.Fatalf("expected commitment checksum %x, got %x",
			expectedCmtChecksum, cmtChecksum)
	}
}
