// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package btcutil

import (
	"fmt"
	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg"
)

func VerifySigScript(sign, hash []byte, chainParams *chaincfg.Params) (*AddressPubKeyHash, error) {
	if len(sign) < btcec.PubKeyBytesLenCompressed {
		return nil, fmt.Errorf("Incorrect signature")
	}
	k, err := btcec.ParsePubKey(sign[:btcec.PubKeyBytesLenCompressed], btcec.S256())
	if err != nil {
		return nil, fmt.Errorf("Incorrect Miner signature. pubkey error")
	}

	pk, _ := NewAddressPubKeyPubKey(*k, chainParams)
	s, err := btcec.ParseSignature(sign[btcec.PubKeyBytesLenCompressed:], btcec.S256())
	if err != nil {
		return nil, fmt.Errorf("Incorrect Miner signature. Signature parse error")
	}

	if !s.Verify(hash, pk.PubKey()) {
		return nil, fmt.Errorf("Incorrect Miner signature. Verification doesn't match")
	}

	return pk.AddressPubKeyHash(), nil
}
