// Copyright (c) 2014-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package consensus

import (
	"fmt"
	"io"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
)

// maxFlagsPerMerkleBlock is the maximum number of flag bytes that could
// possibly fit into a merkle block.  Since each transaction is represented by
// a single bit, this is the max number of transactions per block divided by
// 8 bits per byte.  Then an extra one to cover partials.
const maxFlagsPerMerkleBlock = 50000 / 8

type MsgMerkleBlock struct {
	Header       wire.BlockHeader
	Transactions uint32
	Hashes       []*chainhash.Hash
	Flags        []byte
}

// AddTxHash adds a new transaction hash to the message.
func (msg *MsgMerkleBlock) AddTxHash(hash *chainhash.Hash) error {
	if len(msg.Hashes)+1 > maxTxPerBlock {
		str := fmt.Sprintf("too many tx hashes for message [max %v]",
			maxTxPerBlock)
		return messageError("MsgMerkleBlock.AddTxHash", str)
	}

	msg.Hashes = append(msg.Hashes, hash)
	return nil
}

func (msg *MsgMerkleBlock) BtcDecode(r io.Reader, pver uint32, enc wire.MessageEncoding) error {
	err := readBlockHeader(r, pver, &msg.Header)
	if err != nil {
		return err
	}

	err = readElement(r, &msg.Transactions)
	if err != nil {
		return err
	}

	// Read num block locator hashes and limit to max.
	count, err := ReadVarInt(r, pver)
	if err != nil {
		return err
	}
	if count > maxTxPerBlock {
		str := fmt.Sprintf("too many transaction hashes for message "+
			"[count %v, max %v]", count, maxTxPerBlock)
		return messageError("MsgMerkleBlock.BtcDecode", str)
	}

	// Create a contiguous slice of hashes to deserialize into in order to
	// reduce the number of allocations.
	hashes := make([]chainhash.Hash, count)
	msg.Hashes = make([]*chainhash.Hash, 0, count)
	for i := uint64(0); i < count; i++ {
		hash := &hashes[i]
		err := readElement(r, hash)
		if err != nil {
			return err
		}
		msg.AddTxHash(hash)
	}

	msg.Flags, err = ReadVarBytes(r, pver, maxFlagsPerMerkleBlock,
		"merkle block flags size")
	return err
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgMerkleBlock) BtcEncode(w io.Writer, pver uint32, enc wire.MessageEncoding) error {
	// Read num transaction hashes and limit to max.
	numHashes := len(msg.Hashes)
	if numHashes > maxTxPerBlock {
		str := fmt.Sprintf("too many transaction hashes for message "+
			"[count %v, max %v]", numHashes, maxTxPerBlock)
		return messageError("MsgMerkleBlock.BtcDecode", str)
	}
	numFlagBytes := len(msg.Flags)
	if numFlagBytes > maxFlagsPerMerkleBlock {
		str := fmt.Sprintf("too many flag bytes for message [count %v, "+
			"max %v]", numFlagBytes, maxFlagsPerMerkleBlock)
		return messageError("MsgMerkleBlock.BtcDecode", str)
	}

	err := writeBlockHeader(w, pver, &msg.Header)
	if err != nil {
		return err
	}

	err = writeElement(w, msg.Transactions)
	if err != nil {
		return err
	}

	err = WriteVarInt(w, pver, uint64(numHashes))
	if err != nil {
		return err
	}
	for _, hash := range msg.Hashes {
		err = writeElement(w, hash)
		if err != nil {
			return err
		}
	}

	return WriteVarBytes(w, pver, msg.Flags)
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgMerkleBlock) Command() string {
	return CmdMerkleBlock
}

const MaxBlockPayload  = 80000000

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgMerkleBlock) MaxPayloadLength(pver uint32) uint32 {
	return MaxBlockPayload
}

// NewMsgMerkleBlock returns a new bitcoin merkleblock message that conforms to
// the Message interface.  See MsgMerkleBlock for details.
func NewMsgMerkleBlock(bh *wire.BlockHeader) *MsgMerkleBlock {
	return &MsgMerkleBlock{
		Header:       *bh,
		Transactions: 0,
		Hashes:       make([]*chainhash.Hash, 0),
		Flags:        make([]byte, 0),
	}
}




type MsgTmpMerkleBlock struct {
	Blk MsgMerkleBlock
}

// AddTxHash adds a new transaction hash to the message.
func (msg *MsgTmpMerkleBlock) AddTxHash(hash *chainhash.Hash) error {
	return (&msg.Blk).AddTxHash(hash)
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// This is part of the Message interface implementation.
func (msg *MsgTmpMerkleBlock) BtcDecode(r io.Reader, pver uint32, enc wire.MessageEncoding) error {
	return (&msg.Blk).BtcDecode(r, pver, enc)
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgTmpMerkleBlock) BtcEncode(w io.Writer, pver uint32, enc wire.MessageEncoding) error {
	return (&msg.Blk).BtcEncode(w, pver, enc)
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgTmpMerkleBlock) Command() string {
	return CmdTmpMerkleBlock
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgTmpMerkleBlock) MaxPayloadLength(pver uint32) uint32 {
	return MaxBlockPayload
}

func NewMsgTmpMerkleBlock(bh *wire.BlockHeader) *MsgTmpMerkleBlock {
	return &MsgTmpMerkleBlock{ MsgMerkleBlock{
		Header:       *bh,
		Transactions: 0,
		Hashes:       make([]*chainhash.Hash, 0),
		Flags:        make([]byte, 0),
	},
	}
}