// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package ovm

import (
	"fmt"
)

const (
	MaximumExtraDataSize  uint64 = 32    // Maximum size extra data may be after Genesis.
	QuadCoeffDiv          uint64 = 512   // Divisor for the quadratic particle of the memory cost equation.

	EpochDuration    uint64 = 30000 // Duration between proof-of-work epochs.
	CallCreateDepth  uint64 = 1024  // Maximum depth of call/create stack.
	StackLimit       uint64 = 1024  // Maximum size of VM stack allowed.

	MaxCodeSize = 24576 // Maximum bytecode to permit for a contract

	ModExpQuadCoeffDiv      uint64 = 20     // Divisor for the quadratic particle of the big int modular exponentiation
)

func getCoinStack() stackValidationFunc {
	return func(stack *Stack) error {
		if err := stack.require(2); err != nil {
			return err
		}

		criteria := stack.Back(1)
		x := criteria.Uint64() & 1
		x += (criteria.Uint64() & 2) >> 1

		if err := stack.require(2 + int(x)); err != nil {
			return err
		}

		return nil
	}
}

func makeStackFunc(pop, push int) stackValidationFunc {
	return func(stack *Stack) error {
		if err := stack.require(pop); err != nil {
			return err
		}

		if stack.len()+push-pop > int(StackLimit) {
			return fmt.Errorf("stack limit reached %d (%d)", stack.len(), StackLimit)
		}
		return nil
	}
}

func makeDupStackFunc(n int) stackValidationFunc {
	return makeStackFunc(n, n+1)
}

func makeSwapStackFunc(n int) stackValidationFunc {
	return makeStackFunc(n, n)
}

func validateSpendStack (stack *Stack) error {
	// input stack data layout:
	// tokentype	(a 64 bit value, must be numeric token. i.e., bit-0 = 0. current stack top)
	// value		(a 64 bit value)
	// n-rights		(an optional number of rights, only if bit-1 of token type is set)
	// rights		(n hash values representing rights)

	pop := 2
	push := 1

	if stack.peek().Int64() & 2 != 0 {
		pop += 1 + int(stack.Back(1).Int64())
	}

	if err := stack.require(pop); err != nil {
		return err
	}

	if stack.len()+push-pop > int(StackLimit) {
		return fmt.Errorf("stack limit reached %d (%d)", stack.len(), StackLimit)
	}
	return nil
}

func validateAddTxOutStack (stack *Stack) error {
	// input stack data layout:
	// receiver		(address of receiver.  current stack top)
	// tokentype	(a 64 bit value, must be numeric token. i.e., bit-0 = 0.)
	// value		(a 64 bit value)
	// n-rights		(an optional number of rights, only if bit-1 of token type is set)
	// rights		(n hash values representing rights)

	pop := 3
	push := 1

	if stack.peek().Int64() & 2 != 0 {
		pop += 1 + int(stack.Back(2).Int64())
	}

	if err := stack.require(pop); err != nil {
		return err
	}

	if stack.len()+push-pop > int(StackLimit) {
		return fmt.Errorf("stack limit reached %d (%d)", stack.len(), StackLimit)
	}
	return nil
}

func validateAddTxDefStack (stack *Stack) error {
	// input stack data layout:
	// deftype		(a int value, must be 4. i.e., token.RightDef.)
	// father		(a father hash)
	// attrib		(attribute byte)
	// desclen		(an uint32, length of description)
	// desc			(variable length description)

	pop := 4
	push := 1

	pop += (int(stack.Back(3).Int64()) + 31) >> 5

	if err := stack.require(pop); err != nil {
		return err
	}

	if stack.len()+push-pop > int(StackLimit) {
		return fmt.Errorf("stack limit reached %d (%d)", stack.len(), StackLimit)
	}
	return nil
}
