// This code is available on the terms of the project LICENSE.md file,
// also available online at https://blueoakcouncil.org/license/1.0.0.

package order

import (
	"bytes"
	"fmt"
	"sort"
	"sync"

	"decred.org/dcrdex/dex/msgjson"
	"decred.org/dcrdex/dex/order"
	"github.com/decred/dcrd/crypto/blake256"
)

// EpochQueue represents a client epoch queue.
type EpochQueue struct {
	orders    map[order.OrderID]order.Commitment
	ordersMtx sync.Mutex
}

// NewEpochQueue creates a client epoch queue.
func NewEpochQueue() *EpochQueue {
	return &EpochQueue{
		orders: make(map[order.OrderID]order.Commitment),
	}
}

// Reset clears the epoch queue. This should be called when a new epoch begins.
func (eq *EpochQueue) Reset() {
	eq.ordersMtx.Lock()
	eq.orders = make(map[order.OrderID]order.Commitment)
	eq.ordersMtx.Unlock()
}

// Enqueue appends the provided note to the epoch queue.
func (eq *EpochQueue) Enqueue(note *msgjson.EpochOrderNote) {
	var oid order.OrderID
	copy(oid[:], note.OrderID)

	var commit order.Commitment
	copy(commit[:], note.Commitment)

	eq.ordersMtx.Lock()
	eq.orders[oid] = commit
	eq.ordersMtx.Unlock()
}

// Size...
func (eq *EpochQueue) Size() int {
	eq.ordersMtx.Lock()
	defer eq.ordersMtx.Unlock()
	return len(eq.orders)
}

// Exists checks if the provided order id is in the queue.
func (eq *EpochQueue) Exists(oid order.OrderID) bool {
	eq.ordersMtx.Lock()
	_, ok := eq.orders[oid]
	eq.ordersMtx.Unlock()
	return ok
}

// GenerateMatchProof calculates the sorting seed used in order matching as well
// as the commitment checksum from the provided epoch queue preimages and
// misses.
func (eq *EpochQueue) GenerateMatchProof(preimages []order.Preimage, misses []order.OrderID) ([]byte, []byte, error) {
	eq.ordersMtx.Lock()
	defer eq.ordersMtx.Unlock()

	// Remove all misses.
	for i := range misses {
		delete(eq.orders, misses[i])
	}

	// Map the preimages received with their associated epoch order ids.
	orderPreimages := make(map[order.OrderID]order.Preimage, len(preimages))
	for i := range preimages {
		for oid, commit := range eq.orders {
			commitment := blake256.Sum256(preimages[i][:])
			if commit == commitment {
				orderPreimages[oid] = preimages[i]
				break
			}
		}
	}

	// Ensure all remaining epoch orders matched to a preimage.
	if len(orderPreimages) != len(eq.orders) {
		return nil, nil, fmt.Errorf("expected all remaining epoch orders (%v) "+
			"matched to a preimage (%v)", len(orderPreimages), len(eq.orders))
	}

	// Extract the orders and commitments, and sort them.
	oids := make([]order.OrderID, 0, len(eq.orders))
	commits := make([]order.Commitment, 0, len(eq.orders))
	for oid, commit := range eq.orders {
		oids = append(oids, oid)
		commits = append(commits, commit)
	}

	sort.Slice(oids, func(i, j int) bool {
		return bytes.Compare(oids[i][:], oids[j][:]) < 0
	})

	sort.Slice(commits, func(i, j int) bool {
		return bytes.Compare(commits[i][:], commits[j][:]) < 0
	})

	// Concatenate all preimages per the seed sort index and generate the
	// seed.
	sbuff := make([]byte, 0, len(oids)*order.PreimageSize)
	for i := range oids {
		pimg := orderPreimages[oids[i]]
		sbuff = append(sbuff, pimg[:]...)
	}
	seed := blake256.Sum256(sbuff)

	// Concatenate all order commitments per the commitment sort index and
	// generate the commitment checksum.
	cbuff := make([]byte, 0, len(eq.orders)*order.CommitmentSize)
	for _, commit := range commits {
		cbuff = append(cbuff, commit[:]...)
	}
	csum := blake256.Sum256(cbuff)

	return seed[:], csum[:], nil
}
