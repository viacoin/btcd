// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package wire

import (
	"bytes"
	"io"
	"time"

	"golang.org/x/crypto/scrypt"

	"github.com/viacoin/viad/chaincfg/chainhash"
)

// MaxBlockHeaderPayloadNoAuxpow is the maximum number of bytes a block header can be.
// Version 4 bytes + Timestamp 4 bytes + Bits 4 bytes + Nonce 4 bytes +
// PrevBlock and MerkleRoot hashes.
const MaxBlockHeaderPayloadNoAuxpow = 16 + (chainhash.HashSize * 2)

type MerkleBranch struct {
	Branch []chainhash.Hash
	Index int32
}

type Auxpow struct {
	CoinbaseTxn MsgTx
	ParentBlockHash chainhash.Hash
	CoinbaseBranch MerkleBranch
	BlockchainBranch MerkleBranch
	ParentBlock BlockHeader
}

const BlockVersionAuxpow = 1 << 8
const BlockVersionChainStart = 1 << 16

var MergedMiningHeader = [4]byte{0xfa, 0xbe, 'm', 'm'}

// BlockHeader defines information about a block and is used in the bitcoin
// block (MsgBlock) and headers (MsgHeaders) messages.
type BlockHeader struct {
	// Version of the block.  This is not the same as the protocol version.
	Version int32

	// Hash of the previous block in the block chain.
	PrevBlock chainhash.Hash

	// Merkle tree reference to hash of all transactions for the block.
	MerkleRoot chainhash.Hash

	// Time the block was created.  This is, unfortunately, encoded as a
	// uint32 on the wire and therefore is limited to 2106.
	Timestamp time.Time

	// Difficulty target for the block.
	Bits uint32

	// Nonce used to generate the block.
	Nonce uint32

	Auxpow *Auxpow
}

// BlockHash computes the block identifier hash for the given block header.
func (h *BlockHeader) BlockHash() chainhash.Hash {
	// Encode the header and double sha256 everything prior to the number of
	// transactions.  Ignore the error returns since there is no way the
	// encode could fail except being out of memory which would cause a
	// run-time panic.
	buf := bytes.NewBuffer(make([]byte, 0, MaxBlockHeaderPayloadNoAuxpow))
	_ = writeBlockHeaderNoAuxpow(buf, 0, h)

	return chainhash.DoubleHashH(buf.Bytes())
}

// PoWHash returns the Viacoin scrypt hash of this block header.
// This value is used to check the poW on block advertised network.
func (h *BlockHeader) PowHash() (*chainhash.Hash, error) {
	var powHash chainhash.Hash

	buf := bytes.NewBuffer(make([]byte, 0, MaxBlockHeaderPayloadNoAuxpow))
	_ = writeBlockHeaderNoAuxpow(buf, 0, h)

	scryptHash, err := scrypt.Key(buf.Bytes(), buf.Bytes(), 1024, 1, 1, 32)
	if err != nil {
		return nil, err
	}
	copy(powHash[:], scryptHash)

	return &powHash, nil
}

func (h *BlockHeader) IsAuxpow() bool {
	return h.Version & BlockVersionAuxpow != 0
}

func (h *BlockHeader) GetChainId() uint32 {
	return uint32(h.Version / BlockVersionChainStart)
}

func (h *BlockHeader) SerializeSize() int {
	if !h.IsAuxpow() || h.Auxpow == nil {
		return MaxBlockHeaderPayloadNoAuxpow
	} else {
		return MaxBlockHeaderPayloadNoAuxpow + h.Auxpow.SerializeSize()
	}
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// This is part of the Message interface implementation.
// See Deserialize for decoding block headers stored to disk, such as in a
// database, as opposed to decoding block headers from the wire.
func (h *BlockHeader) BtcDecode(r io.Reader, pver uint32, enc MessageEncoding) error {
	return readBlockHeader(r, pver, h)
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
// See Serialize for encoding block headers to be stored to disk, such as in a
// database, as opposed to encoding block headers for the wire.
func (h *BlockHeader) BtcEncode(w io.Writer, pver uint32, enc MessageEncoding) error {
	return writeBlockHeader(w, pver, h)
}

// Deserialize decodes a block header from r into the receiver using a format
// that is suitable for long-term storage such as a database while respecting
// the Version field.
func (h *BlockHeader) Deserialize(r io.Reader) error {
	// At the current time, there is no difference between the wire encoding
	// at protocol version 0 and the stable long-term storage format.  As
	// a result, make use of readBlockHeader.
	return readBlockHeader(r, 0, h)
}

// Serialize encodes a block header from r into the receiver using a format
// that is suitable for long-term storage such as a database while respecting
// the Version field.
func (h *BlockHeader) Serialize(w io.Writer) error {
	// At the current time, there is no difference between the wire encoding
	// at protocol version 0 and the stable long-term storage format.  As
	// a result, make use of writeBlockHeader.
	return writeBlockHeader(w, 0, h)
}

// NewBlockHeader returns a new BlockHeader using the provided version, previous
// block hash, merkle root hash, difficulty bits, and nonce used to generate the
// block with defaults for the remaining fields.
func NewBlockHeader(version int32, prevHash, merkleRootHash *chainhash.Hash,
	bits uint32, nonce uint32) *BlockHeader {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &BlockHeader{
		Version:    version,
		PrevBlock:  *prevHash,
		MerkleRoot: *merkleRootHash,
		Timestamp:  time.Unix(time.Now().Unix(), 0),
		Bits:       bits,
		Nonce:      nonce,
	}
}

// readBlockHeader reads a bitcoin block header from r.  See Deserialize for
// decoding block headers stored to disk, such as in a database, as opposed to
// decoding from the wire.
func readBlockHeader(r io.Reader, pver uint32, bh *BlockHeader) error {
	err := readElements(r, &bh.Version, &bh.PrevBlock, &bh.MerkleRoot,
		(*uint32Time)(&bh.Timestamp), &bh.Bits, &bh.Nonce)
	if err != nil {
		return err
	}
	return readAuxpow(r, pver,  bh)
}

func readAuxpow(r io.Reader, pver uint32, bh *BlockHeader) error {
	if !bh.IsAuxpow() {
		return nil
	}

	ap := Auxpow{}

	err := ap.CoinbaseTxn.DeserializeNoWitness(r)
	if err != nil {
		return err
	}

	err = readElement(r, &ap.ParentBlockHash)
	if err != nil {
		return err
	}

	count, err := ReadVarInt(r, pver)
	if err != nil {
		return err
	}

	ap.CoinbaseBranch.Branch = make([]chainhash.Hash, count)
	for i := uint64(0); i < count; i++ {
		err = readElement(r, &ap.CoinbaseBranch.Branch[i])
		if err != nil {
			return err
		}
	}
	err = readElement(r, &ap.CoinbaseBranch.Index)
	if err != nil {
		return err
	}

	count, err = ReadVarInt(r, pver)
	if err != nil {
		return err
	}

	ap.BlockchainBranch.Branch = make([]chainhash.Hash, count)
	for i := uint64(0); i < count; i++ {
		err = readElement(r, &ap.BlockchainBranch.Branch[i])
		if err != nil {
			return err
		}
	}
	err = readElement(r, &ap.BlockchainBranch.Index)
	if err != nil {
		return err
	}

	err = readElements(r, &ap.ParentBlock.Version, &ap.ParentBlock.PrevBlock, &ap.ParentBlock.MerkleRoot,
		(*uint32Time)(&ap.ParentBlock.Timestamp), &ap.ParentBlock.Bits, &ap.ParentBlock.Nonce)
	if err != nil {
		return err
	}

	bh.Auxpow = &ap
	return nil
}

func writeBlockHeaderNoAuxpow(w io.Writer, pver uint32, bh *BlockHeader) error {
	sec := uint32(bh.Timestamp.Unix())
	return writeElements(w, bh.Version, &bh.PrevBlock, &bh.MerkleRoot,
		sec, bh.Bits, bh.Nonce)
}

// writeBlockHeader writes a bitcoin block header to w.  See Serialize for
// encoding block headers to be stored to disk, such as in a database, as
// opposed to encoding for the wire.
func writeBlockHeader(w io.Writer, pver uint32, bh *BlockHeader) error {
	err := writeBlockHeaderNoAuxpow(w, pver, bh)
	if err != nil {
		return err
	}
	if bh.IsAuxpow() {
		return writeAuxpow(w, pver, bh)
	}
	return nil
}

func writeAuxpow(w io.Writer, pver uint32, bh *BlockHeader) error {
	if bh.Auxpow == nil {
		return nil
	}

	err := bh.Auxpow.CoinbaseTxn.SerializeNoWitness(w)
	if err != nil {
		return err
	}

	err = writeElement(w, bh.Auxpow.ParentBlockHash)
	if err != nil {
		return err
	}

	count := uint64(len(bh.Auxpow.CoinbaseBranch.Branch))
	err = WriteVarInt(w, pver, count)
	if err != nil {
		return err
	}

	for i := uint64(0); i < count; i++ {
		err = writeElement(w, bh.Auxpow.CoinbaseBranch.Branch[i])
		if err != nil {
			return err
		}
	}
	err = writeElement(w, bh.Auxpow.CoinbaseBranch.Index)
	if err != nil {
		return err
	}

	count = uint64(len(bh.Auxpow.BlockchainBranch.Branch))
	err = WriteVarInt(w, pver, count)
	if err != nil {
		return err
	}

	for i := uint64(0); i < count; i++ {
		err = writeElement(w, bh.Auxpow.BlockchainBranch.Branch[i])
		if err != nil {
			return err
		}
	}
	err = writeElement(w, bh.Auxpow.BlockchainBranch.Index)
	if err != nil {
		return err
	}

	return writeBlockHeaderNoAuxpow(w, pver, &bh.Auxpow.ParentBlock)
}

func (b *MerkleBranch) Check(hash chainhash.Hash) chainhash.Hash {
	if b.Index == -1 {
		return chainhash.Hash{}
	}

	var scratch []byte
	idx := b.Index
	thash := hash

	for _, it := range b.Branch {
		if idx & 1 != 0 {
			scratch = append(scratch, it[:]...)
			scratch = append(scratch, thash[:]...)
		} else {
			scratch = append(scratch, thash[:]...)
			scratch = append(scratch, it[:]...)
		}
		thash = chainhash.DoubleHashH(scratch)
		scratch = nil
		idx >>= 1
	}
	return thash
}

func(b *MerkleBranch) CheckAndRevert(hash chainhash.Hash) chainhash.Hash {
	thash := b.Check(hash)
	// revert
	for i, j := 0, chainhash.HashSize - 1; i < j; i, j = i + 1, j - 1 {
		thash[i], thash[j] = thash[j], thash[i]
	}
	return thash
}

func (a *Auxpow) SerializeSize() int {
	return a.CoinbaseTxn.SerializeSizeStripped() +
		chainhash.HashSize * (1 + len(a.CoinbaseBranch.Branch) + len(a.BlockchainBranch.Branch)) +
		VarIntSerializeSize(uint64(len(a.CoinbaseBranch.Branch))) +
		VarIntSerializeSize(uint64(len(a.BlockchainBranch.Branch))) +
		4 * 2 +
		MaxBlockHeaderPayloadNoAuxpow
}

func GetBlockHeaderSize(raw []byte) int {
	pr := bytes.NewBuffer(raw)
	h := BlockHeader{}
	readBlockHeader(pr, 60002, &h)
	return h.SerializeSize()
}
