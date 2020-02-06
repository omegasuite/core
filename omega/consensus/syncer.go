package consensus

import (
	"bytes"
	"fmt"
	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcd/wire/common"
	"github.com/btcsuite/btcutil"
	"github.com/btcsuite/omega/token"
	"net/http"
	"reflect"
	"sync"
	"time"
)

type tree struct {
	creator  [20]byte
	fees uint64
	hash chainhash.Hash
	header * wire.BlockHeader
	block * btcutil.Block
}

type Syncer struct {
	// one syncer handles one height level consensus
	// if current height is below the best chain, syncer quits
	// if current height is more than the best chain, syncer wait, but accept all incoming messages
	// syncer quits when it finishes one height level and the block is connected to the main chain

	Runnable bool

	Committee int32
	Base int32

	Members map[[20]byte]int32
	Names map[int32][20]byte

	Me [20]byte
	Myself int32

	Malice map[[20]byte]struct {}

	// a node may annouce to be a candidate if he believes he is the best choice
	// if he believes another one is the best, send his knowledge about the best to the best

	// a node received the candidacy announcement returns an agree message if he believes
	// the node is better than himself and is known to more than 1/ nodes (qualified)
	// a node rejects candidacy announcement should send his knowledge of the best in reply

	// a node collected more than 1/2 agrees may annouce the fact by broadcasting the agreements it
	// collected.

	asked    map[int32]struct{}		// those who have asked to be a candidate, and not released
	agrees   map[int32]struct{}		// those who I have agree to be a candidate
	agreed   int32			// the one who I have agreed. can not back out until released by
	sigGiven int32			// who I have given my signature. can never change once given.

//	consents map[[20]byte]int32		// those wh
	forest   map[[20]byte]*tree		// blocks mined
	knows    map[[20]byte][]*wire.MsgKnowledge	// the knowledges we received organized by finders (i.e. fact)
	signed   map[[20]byte]struct{}

	knowledges *Knowledgebase

	newtree chan tree
	quit chan struct{}
	Done bool

	Height int32

	pending map[string][]Message
	pulling map[int32]struct{}

	messages chan Message
	
	mutex sync.Mutex

	// debug only
	knowRevd	[]int32
	candRevd	[]int32
	consRevd	[]int32

	// wait for end of task
	wg          sync.WaitGroup
}

func (self *Syncer) repeater() {
	going := true
	idles := 0
	for going {
		select {
		case <-self.quit:
			going = false

		default:
			// resend knowledge
			allm := int64(0)
			for _, k := range self.knowledges.Knowledge[self.Myself] {
				allm |= k
			}

			if allm == 0 {
				time.Sleep(time.Millisecond * 200)
				continue
			}

			sent := false

			for i, k := range self.knowledges.Knowledge[self.Myself] {
				if int32(i) == self.Myself || k == allm {
					continue
				}
//				if k == self.knowledges.Knowledge[self.Myself][self.Myself] {
//					continue
//				}
				// about my tree, if I know somthing someone does not know, send him info
				k = k ^ allm
				if k == 0 {
					continue
				}

				self.mutex.Lock()
				for _, p := range self.knows[self.Me] {
					m := int64((1 << self.Myself) | (1 << i))
					for _, r := range p.K {
						m |= 0x1 << r
					}
					if k & m != 0 && p.K[len(p.K) - 1] != int64(i) {
						// send it
						pp := *p
						pp.K = append(pp.K, int64(self.Myself))
						pp.From = self.Me
						if miner.server.CommitteeMsg(self.Names[int32(i)], &pp) {
							self.knowledges.Knowledge[self.Myself][i] |= m
							self.knowledges.Knowledge[self.Myself][self.Myself] |= m
						}
						k ^= m
						sent = true
						if k == 0 {
							break
						}
					}
				}
				self.mutex.Unlock()
			}
			if self.knowledges.Qualified(self.Myself) && (self.agreed == -1 || self.agreed == self.Myself) {
				self.candidacy()
				self.ckconsensus()
			} else if !sent {
				idles++
			}
			if idles > 2 {
				// force to resend
				for i, _ := range self.knowledges.Knowledge[self.Myself] {
					if int32(i) == self.Myself {
						continue
					}

					self.mutex.Lock()
					for _, p := range self.knows[self.Me] {
						m := int64((1 << self.Myself) | (1 << i))
						for _, r := range p.K {
							m |= 0x1 << r
						}
						if p.K[len(p.K) - 1] != int64(i) {
							// send it
							pp := *p
							pp.K = append(pp.K, int64(self.Myself))
							pp.From = self.Me
							if miner.server.CommitteeMsg(self.Names[int32(i)], &pp) {
								self.knowledges.Knowledge[self.Myself][i] |= m
								self.knowledges.Knowledge[self.Myself][self.Myself] |= m
							}
						}
					}
					self.mutex.Unlock()
				}
			}
			time.Sleep(time.Millisecond * 200)
		}
	}
	self.wg.Done()
}

func (self *Syncer) run() {
	going := true

	go self.repeater()

	defer self.wg.Done()

	for going {
		select {
		case tree := <- self.newtree:
			if self.sigGiven >= 0 {
				continue
			}
			log.Infof("newtree %s at %d", tree.hash.String(), self.Height)
			if !self.validateMsg(tree.creator, nil, nil) {
				log.Infof("tree creator %x is not a member of committee", tree.creator)
				continue
			}

			if _, ok := self.forest[tree.creator]; !ok || self.forest[tree.creator].block == nil {
				// each creator may submit only one tree
				self.forest[tree.creator] = &tree
//				c := self.Members[tree.creator]
//				self.knowledges.ProcessTree(c)
/*
				if pend, ok := self.pending[wire.CmdBlock]; ok {	// is that ok?
					delete(self.pending, wire.CmdBlock)
					for _,m := range pend {
						log.Infof("processing pending message %s", m.(wire.Message).Command())
						self.messages <- m
					}
				}
 */
			} else if (self.forest[tree.creator].hash != chainhash.Hash{}) && tree.hash != self.forest[tree.creator].hash {
				if self.Me == tree.creator {
					log.Errorf("Incorrect tree. I generated dup tree hash at %d", self.Height)
					break
				}
				self.Malice[tree.creator] = struct {}{}
				delete(self.forest, tree.creator)
				c := self.Members[tree.creator]
				self.knowledges.Malice(c)
			}

			if bytes.Compare(tree.creator[:], self.Me[:]) == 0 {
				k := wire.NewMsgKnowledge()
				k.From = self.Me
				k.Height = self.Height
				k.Finder = self.Me
				k.M = tree.hash
				k.K = []int64{int64(self.Myself)}
				self.messages <- k
			}
			self.print()

		case m := <- self.messages:
			log.Infof("processing %s message", reflect.TypeOf(m).String())
			switch m.(type) {
			case *wire.MsgKnowledge:		// passing knowledge
				if self.sigGiven >= 0 {
					continue
				}

				k := m.(*wire.MsgKnowledge)

				self.knowRevd[self.Members[k.From]] = self.Members[k.From]

				log.Infof("MsgKnowledge: Finder = %x\nFrom = %x\nHeight = %d\nM = %s\nK = [%v]",
					k.Finder, k.From, k.Height, k.M.String(), k.K)
				if !self.validateMsg(k.Finder, &k.M, m) {
					log.Infof("MsgKnowledge invalid")
					continue
				}

				self.mutex.Lock()
				if self.knows[k.Finder] == nil {
					self.knows[k.Finder] = make([]*wire.MsgKnowledge, 0)
				}
				self.knows[k.Finder] = append(self.knows[k.Finder], k)
				self.mutex.Unlock()

				if self.knowledges.ProcKnowledge(k) {
					self.candidacy()
				}

			case *wire.MsgCandidate:		// announce candidacy
				if self.sigGiven >= 0 {
					continue
				}
				k := m.(*wire.MsgCandidate)

				self.candRevd[self.Members[k.F]] = self.Members[k.F]

				log.Infof("MsgCandidate: M = %s\nHeight = %d\nF = %x\nSignature = %x\n",
					k.M.String(), k.Height, k.F, k.Signature)
				if !self.validateMsg(k.F, &k.M, m) {
					log.Infof("Invalid MsgCandidate message")
					continue
				}
				self.Candidate(k)

			case *wire.MsgCandidateResp:		// response to candidacy announcement
				if self.sigGiven >= 0 {
					continue
				}
				k := m.(*wire.MsgCandidateResp)
				if !self.validateMsg(k.From, nil, m) {
					continue
				}
				self.candidateResp(k)

			case *wire.MsgRelease:			// grant a release from duty
				if self.sigGiven >= 0 {
					continue
				}
				k := m.(*wire.MsgRelease)
				if !self.validateMsg(k.From, nil, m) {
					continue
				}
				self.Release(k)

			case *wire.MsgConsensus:			// announce consensus reached
				if self.sigGiven >= 0 {
					continue
				}
				k := m.(*wire.MsgConsensus)
				self.consRevd[self.Members[k.From]] = self.Members[k.From]

				if !self.validateMsg(k.From, nil, m) {
					continue
				}
				self.Consensus(k)

			case *wire.MsgSignature:		// received signature
				if self.Signature(m.(*wire.MsgSignature)) {
					going = false
				}
			}
			self.print()

		case <-self.quit:
			going = false
		}
	}

	for true {
		select {
		case <-self.newtree:
		case m := <- self.messages:
			switch m.(type) {
			case *wire.MsgSignature:
				log.Info("handling MsgSignature on quit")
				self.Signature(m.(*wire.MsgSignature))
			}

		default:
			if self.sigGiven != -1 {
				owner := self.Names[self.sigGiven]
				if self.Runnable && self.forest[owner] != nil && self.forest[owner].block != nil &&
					len(self.forest[owner].block.MsgBlock().Transactions[0].SignatureScripts) > wire.CommitteeSize/2+1 {
					log.Info("passing NewConsusBlock on quit")
					miner.server.NewConsusBlock(self.forest[owner].block)
				}
			}
			self.Done = true
			self.Runnable = false
//			close(self.messages)
//			self.Names, self.forest, self.Malice, self.consents = nil, nil, nil, nil
//			self.Members, self.pending, self.signed, self.agrees = nil, nil, nil, nil
//			self.pulling, self.knowledges = nil, nil
			log.Infof("sync %d quit", self.Height)
			return
		}
	}
}

/*
func (self *Syncer) releasenb() {
	self.Runnable = false
	self.Quit()

	miner.server.NewConsusBlock(self.forest[self.Me].block)

	cleaner(self.Height)
}

 */

func (self *Syncer) Signature(msg * wire.MsgSignature) bool {
	tree := int32(-1)
	for i,f := range self.forest {
		if msg.M == f.hash && i == msg.For && f.block != nil {
			tree = self.Members[i]
		}
	}
	if tree < 0 || (self.sigGiven != -1 && self.sigGiven != tree) {
		log.Infof("signature ignored, it is for %d (%s), not what I gave %d.", tree, msg.M.String(), self.sigGiven)
		return false
	}

	self.sigGiven = tree
	owner := self.Names[tree]

	// TODO: detect double signature
	// verify signature
	hash := blockchain.MakeMinerSigHash(self.Height, msg.M)

	k,err := btcec.ParsePubKey(msg.Signature[:btcec.PubKeyBytesLenCompressed], btcec.S256())
	if err != nil {
		return false
	}

	s, err := btcec.ParseDERSignature(msg.Signature[btcec.PubKeyBytesLenCompressed:], btcec.S256())
	if err != nil {
		return false
	}

	if !s.Verify(hash, k) {
		return false
	}

	if len(self.forest[owner].block.MsgBlock().Transactions[0].SignatureScripts[1]) <= 20 {
		// remove the sig 1 that contained the miner's name
		self.forest[owner].block.MsgBlock().Transactions[0].SignatureScripts =
			self.forest[owner].block.MsgBlock().Transactions[0].SignatureScripts[:1]
	}

	self.forest[owner].block.MsgBlock().Transactions[0].SignatureScripts = append(
		self.forest[owner].block.MsgBlock().Transactions[0].SignatureScripts,
		msg.Signature[:])
	self.signed[msg.From] = struct{}{}

	return len(self.signed) > wire.CommitteeSize / 2
}

func (self *Syncer) Consensus(msg * wire.MsgConsensus) {
	if self.agreed == self.Members[msg.From] && self.sigGiven == -1 {
		// verify signature
		hash := blockchain.MakeMinerSigHash(self.Height, self.forest[msg.From].hash)

		k,err := btcec.ParsePubKey(msg.Signature[:btcec.PubKeyBytesLenCompressed], btcec.S256())
		if err != nil {
			return
		}

		s, err := btcec.ParseDERSignature(msg.Signature[btcec.PubKeyBytesLenCompressed:], btcec.S256())
		if err != nil {
			return
		}

		if !s.Verify(hash, k) {
			return
		}

		if privKey := miner.server.GetPrivKey(self.Me); privKey != nil {
			self.sigGiven = self.agreed
			sig, _ := privKey.Sign(hash)
			sigmsg := wire.MsgSignature {
				For:	   msg.From,
			}
			sigmsg.MsgConsensus = wire.MsgConsensus {
				Height:    self.Height,
				From:      self.Me,
				M:		   msg.M,
			}

			s := sig.Serialize()
			copy(sigmsg.Signature[:], privKey.PubKey().SerializeCompressed())
			copy(sigmsg.Signature[btcec.PubKeyBytesLenCompressed:], s)

			miner.server.CommitteeCastMG(self.Me, &sigmsg, self.Height)

			if self.forest[msg.From].block != nil {
				// remove the sig 1 that contained the miner's name
				self.forest[msg.From].block.MsgBlock().Transactions[0].SignatureScripts =
					self.forest[msg.From].block.MsgBlock().Transactions[0].SignatureScripts[:1]

				// add signatures to block
				self.forest[msg.From].block.MsgBlock().Transactions[0].SignatureScripts = append(
					self.forest[msg.From].block.MsgBlock().Transactions[0].SignatureScripts,
					msg.Signature[:])
				self.forest[msg.From].block.MsgBlock().Transactions[0].SignatureScripts = append(
					self.forest[msg.From].block.MsgBlock().Transactions[0].SignatureScripts,
					sigmsg.Signature[:])
				self.signed[msg.From] = struct{}{}
				self.signed[self.Me] = struct{}{}
			}
		}
	}
}

func (self *Syncer) Release(msg * wire.MsgRelease) {
	delete(self.asked, self.Members[msg.From])

	if self.agreed == self.Members[msg.From] {
		self.knowledges.ProcFlatKnowledge(msg.Better, msg.K)

		if _, ok := self.forest[self.Names[msg.Better]]; !ok {
			self.pull(msg.M, msg.Better)
//			self.pending[wire.CmdBlock] = append(self.pending[wire.CmdBlock], msg)
//			return
		}
		self.agreed = -1
/*
		self.agreed = msg.Better
		d := wire.MsgCandidateResp{Height: msg.Height, K: self.makeAbout(msg.Better).K,
			M:self.makeAbout(msg.Better).M, Better: msg.Better,
			From: self.Me, Reply:"cnst"}
		miner.server.CommitteeMsg(msg.Better, &d)
 */
	}
}

func (self *Syncer) ckconsensus() {
	if self.agreed != self.Myself || len(self.agrees) + 1 <= wire.CommitteeSize / 2 {
		return
	}

	if self.forest[self.Me] == nil || self.forest[self.Me].block == nil {
		return
	}

	hash := blockchain.MakeMinerSigHash(self.Height, self.forest[self.Me].hash)

	if privKey := miner.server.GetPrivKey(self.Me); privKey != nil && self.sigGiven == -1 {
		self.sigGiven = self.Myself

		sig, _ := privKey.Sign(hash)
		msg := wire.MsgConsensus{
			Height:    self.Height,
			From:      self.Me,
			M:		   self.forest[self.Me].hash,
		}

		copy(msg.Signature[:], privKey.PubKey().SerializeCompressed())
		copy(msg.Signature[btcec.PubKeyBytesLenCompressed:], sig.Serialize())

		self.forest[self.Me].block.MsgBlock().Transactions[0].SignatureScripts =
			self.forest[self.Me].block.MsgBlock().Transactions[0].SignatureScripts[:1]

		self.forest[self.Me].block.MsgBlock().Transactions[0].SignatureScripts = append(
			self.forest[self.Me].block.MsgBlock().Transactions[0].SignatureScripts,
			msg.Signature[:])
		self.signed[self.Me] = struct{}{}

		miner.server.CommitteeCastMG(self.Me, &msg, self.Height)
	}
}

func (self *Syncer) makeRelease(better int32) *wire.MsgRelease {
	return &wire.MsgRelease{
		Better: better,
		K:      self.makeAbout(better).K,
		M:      self.makeAbout(better).M,
		Height: self.Height,
		From:   self.Me,
	}
}

func (self *Syncer) dupKnowledge(fmp int32) {
	agreed := self.Names[self.agreed]

	ns := make([]*wire.MsgKnowledge, 0)

	self.mutex.Lock()
	defer self.mutex.Unlock()

	for _, ks := range self.knows[agreed] {
		if ks.K[len(ks.K)-1] != int64(fmp) {
			m := int64((1 << fmp) | (1 << self.Myself))
			for _, w := range ks.K {
				m = 1 << w
			}

			t := *ks
			t.K = append(t.K, int64(self.Myself))

			if self.knowledges.gain(self.agreed, t.K) {
				if miner.server.CommitteeMsg(self.Names[fmp], &t) {
					self.knowledges.Knowledge[self.agreed][fmp] |= m
					self.knowledges.Knowledge[self.agreed][self.Myself] |= m
				}
				ns = append(ns, &t)
			}
		}
	}
	self.knows[agreed] = append(self.knows[agreed], ns...)
}

func (self *Syncer) yield(better int32) bool {
	if self.better(better, self.agreed) {
		delete(self.asked, self.Myself)
		rls := self.makeRelease(better)
		for r, _ := range self.agrees {
			miner.server.CommitteeMsgMG(self.Names[r], rls, self.Height)
		}
		self.agrees = make(map[int32]struct{})
		self.agreed = -1
		if _, ok := self.asked[better]; ok {
			// give a consent to Better
			d := wire.MsgCandidateResp{Height: self.Height, K: []int64{}, From: self.Me}
			d.Reply = "cnst"
			d.Better = better
			d.M = self.forest[self.Names[better]].hash
			self.agreed = better
			miner.server.CommitteeMsgMG(self.Names[better], &d, self.Height)
		}
		return true
	}
	return false
}

func (self *Syncer) candidateResp(msg *wire.MsgCandidateResp) {
	if msg.Reply == "cnst" {
		if self.agreed != self.Myself {
			// release the node from obligation and notify him about new agreed
			log.Infof("consent received from %x but I am not taking it", msg.From)
			miner.server.CommitteeMsgMG(msg.From, self.makeRelease(self.agreed), self.Height)
		} else {
			log.Infof("consent received from %x", msg.From)
			self.agrees[self.Members[msg.From]] = struct{}{}
			self.ckconsensus()
		}
	} else if msg.Reply == "rjct" && self.agreed == self.Myself {
		log.Infof("rejection received from %x", msg.From)

		switch msg.Better {
		case -1:
			// reject because not in committee. can't help
			return

		case -2:
			// reject because not Qualified. send knowledge about what I have agreed
			self.dupKnowledge(self.Members[msg.From])
			break

		default:
			t,ok := self.forest[self.Names[msg.Better]]
			if !ok || t.block == nil{
				self.pull(msg.M, msg.Better)
//				self.pending[wire.CmdBlock] = append(self.pending[wire.CmdBlock], msg)
				return
			}
			// check if Better is indeed better, if yes, release it (in yield)
			if !self.yield(msg.Better) {
				// no. we are better, send knowledge about it
				self.dupKnowledge(self.Members[msg.From])
				if self.agreed == self.Myself {
					msg := wire.NewMsgCandidate(self.Height, self.Me, self.forest[self.Me].hash)
					miner.server.CommitteeMsgMG(self.Me, msg, self.Height) // ask again
				}
			}
		}
/*
		if self.knowledges.ProcFlatKnowledge(msg.Better, msg.K) {
			// gained more knowledge, check if we are better than msg.better, if not
			// release nodes in agrees
			if self.knowledges.Qualified(msg.Better) &&
				self.asked[self.Names[msg.Better]] &&
				self.forest[self.Names[msg.Better]].fees > self.forest[self.Me].fees ||
				(self.forest[self.Names[msg.Better]].fees == self.forest[self.Me].fees && msg.Better > self.Myself) {

				delete(self.asked, self.Me)
				for r, _ := range self.agrees {
					miner.server.CommitteeMsg(r, self.makeRelease(msg.Better))
				}

				self.agreed = msg.Better
				d := wire.MsgCandidateResp{Height: msg.Height,
					K: self.makeAbout(msg.Better).K,
					M: self.makeAbout(msg.Better).M,
					Better: msg.Better,
					From: self.Me, Reply:"cnst"}
				miner.server.CommitteeMsg(msg.Better, &d)

				self.agrees = make(map[int32]struct{})
				return
			}
		}

 */
	}
}

func (self *Syncer) candidacy() {
	if (self.agreed != -1 && self.agreed != self.Myself) || !self.knowledges.Qualified(self.Myself) {
		return
	}

	better := self.Myself

	for i := 0; i < wire.CommitteeSize; i++ {
		if _,ok := self.asked[int32(i)]; ok && self.better(int32(i), better) && self.knowledges.Qualified(int32(i)) {
			// there is a better candidate
			// someone else is the best, send him knowledge about him that he does not know
			better = int32(i)
		}
	}

	if better != self.Myself {
		t := self.forest[self.Names[better]]
		if t.block == nil {
			self.pull(t.hash, better)
		}
		return
	}

	mp := self.Myself
	self.agreed = mp

//	self.consents[self.Me] = 1

	log.Infof("Announce candicacy by %d", self.Myself)
	self.DebugInfo()

	msg := wire.NewMsgCandidate(self.Height, self.Me, self.forest[self.Me].hash)

	self.asked[self.Myself] = struct{}{}

	miner.server.CommitteeCastMG(self.Me, msg, self.Height)
}

func (self *Syncer) Candidate(msg *wire.MsgCandidate) {
	// received a request for confirmation of candidacy
	d := wire.MsgCandidateResp{Height: msg.Height, K: []int64{}, From: self.Me, M: msg.M}

	from := msg.F
	fmp := self.Members[from]
	self.asked[fmp] = struct{}{}

	if _,ok := self.Members[from]; !self.Runnable || !ok {
		d.Reply = "rjct"
		d.Better = -1
		miner.server.CommitteeMsgMG(self.Names[fmp], &d, self.Height)
		return
	}

	if _,ok := self.forest[from]; !ok || self.forest[from].block == nil {
		self.pull(msg.M, fmp)
//		self.pending[wire.CmdBlock] = append(self.pending[wire.CmdBlock], msg)
//		return
	}

	if self.sigGiven != -1 {
		// should never be here. so ignore it
		return
	}

	if !self.knowledges.Qualified(fmp) {
		d.Reply = "rjct"
		d.Better = -2
		miner.server.CommitteeMsgMG(self.Names[fmp], &d, self.Height)
		return
	}

	if self.agreed != -1 && self.better(fmp, self.agreed) && self.yield(fmp) {
		return
	}

	if self.agreed == -1 || self.agreed == fmp {
		log.Infof("consent given by %x to %d", self.Me, fmp)
		d.Reply = "cnst"
		d.Better = fmp
		self.agreed = fmp
		miner.server.CommitteeMsgMG(self.Names[fmp], &d, self.Height)
		return
	}

	// reject it, tell who we have agreed. check whether fmp is better than agreed, if not give knowledge of agreed,
	if self.better(self.agreed, fmp) {
		self.dupKnowledge(fmp)
	}

	d.Reply = "rjct"
	//		d.K = []int64{-1024}
	//		for _,p := range self.knowledges.Knowledge[self.agreed] {
	//			d.K = append(d.K, p)
	//		}
	d.Better = self.agreed
	d.M = self.forest[self.Names[self.agreed]].hash
	miner.server.CommitteeMsgMG(self.Names[fmp], &d, self.Height)
}

func CreateSyncer(h int32) *Syncer {
	p := Syncer{}

	p.quit = make(chan struct{})
	p.Height = h
	p.pending = make(map[string][]Message, 0)
	p.newtree = make(chan tree, wire.CommitteeSize * 3)	// will hold trees before runnable
	p.messages = make(chan Message, wire.CommitteeSize * 3)
	p.pulling = make(map[int32]struct{})
	p.agrees = make(map[int32]struct{})
	p.asked = make(map[int32]struct{})
	p.signed = make(map[[20]byte]struct{})
	p.Members = make(map[[20]byte]int32)
	p.Names = make(map[int32][20]byte)
	p.Malice = make(map[[20]byte]struct {})
	p.knows = make(map[[20]byte][]*wire.MsgKnowledge)

	p.agreed = -1
	p.sigGiven = -1
//	p.mutex = sync.Mutex{}

//	p.consents = make(map[[20]byte]int32, wire.CommitteeSize)
	p.forest = make(map[[20]byte]*tree, wire.CommitteeSize)

	p.Runnable = false
//	p.Me = miner.name

//	p.SetCommittee()
	p.knowRevd = make([]int32, wire.CommitteeSize)
	p.candRevd = make([]int32, wire.CommitteeSize)
	p.consRevd = make([]int32, wire.CommitteeSize)
	for i := 0; i < wire.CommitteeSize; i++ {
		p.knowRevd[i], p.candRevd[i], p.consRevd[i] = -1, -1, -1
	}

	return &p
}

func (self *Syncer) validateMsg(finder [20]byte, m * chainhash.Hash, msg Message) bool {
	if !self.Runnable || self.Done {
		log.Infof("validate failed. I'm not runnable")
		time.Sleep(time.Second)
//		self.pending[wire.CmdBlock] = append(self.pending[wire.CmdBlock], msg)
//		return false
	}

	if _, ok := self.Malice[finder]; ok {
		log.Infof("validate failed. %x is a malice node", finder)
		return false
	}

	c, ok := self.Members[finder]

	if !ok {
		log.Infof("validate failed. %x is a not a member", finder)
		return false
	}

	if _, ok = self.forest[finder]; m != nil && !ok {
		self.forest[finder] = &tree{
			creator: finder,
			fees:    0,
			hash:    *m,
			header:  nil,
			block:   nil,
		}
//		if _, ok := self.pending[wire.CmdBlock]; !ok {
//			self.pending[wire.CmdBlock] = make([]Message, 0)
//		}
//		self.pending[wire.CmdBlock] = append(self.pending[wire.CmdBlock], msg)

		log.Infof("Pull block %s from %d", m.String(), c)
		self.pull(*m, c)
		return true
	}

	if m != nil && self.forest[finder].hash != *m {
		log.Infof("block is not the same as registered %x", self.forest[finder].hash)
//		self.Malice[finder] = struct {}{}
//		delete(self.forest, finder)
//		self.knowledges.Malice(c)
		return false
	}
	return true
}

func (self *Syncer) SetCommittee() {
	if self.Runnable {
		return
	}

	self.mutex.Lock()
	defer self.mutex.Unlock()

	best := miner.server.BestSnapshot()
	self.Runnable = self.Height == best.Height + 1

	if !self.Runnable {
		return
	}

	c := int32(best.LastRotation)
	log.Infof("SetCommittee for %d", c)

	self.Committee = c
	self.Base = c - wire.CommitteeSize + 1

	copy(self.Me[:], miner.name[:])

	log.Infof("My name in committee is %x", self.Me)
	in := false

	for i := c - wire.CommitteeSize + 1; i <= c; i++ {
		blk,_ := miner.server.MinerBlockByHeight(i)
		if blk == nil {
			return
		}
		var adr [20]byte
		copy(adr[:], blk.MsgBlock().Miner)
		who := i - (c - wire.CommitteeSize + 1)

		self.Members[adr] = who
		self.Names[who] = adr

		if bytes.Compare(self.Me[:], adr[:]) == 0 {
			self.Myself = who
			in = true
			log.Infof("My local designation in committee is %d", self.Myself)
		}

		log.Infof("Member %d is %x", who, adr)
	}

	self.knowledges = CreateKnowledge(self)

	if in {
		log.Infof("Consensus running block at %d", self.Height)
		go self.run()
		self.wg.Add(2)
	}

//	miner.updateheight <- self.Height
}

func (self *Syncer) UpdateChainHeight(h int32) {
	if h < self.Height {
		return
	}
	if h > self.Height {
		self.Quit()
		return
	}
	
//	self.SetCommittee()
}

/*
func (self *Syncer) HeaderInit(block *MsgMerkleBlock) {
	var adr [20]byte
	copy(adr[:], block.From[:])

	if !self.Runnable {
		best := self.Chain.BestSnapshot()

		if best.Height > self.Height {
			self.Quit()
			return
		}

		self.Runnable = self.Height == best.Height + 1

		if self.Runnable {
			self.SetCommittee(int32(best.LastRotation))
		}
	}

	self.newtree <- tree {
		creator: adr,
		fees: block.Fees,
		hash: block.Header.BlockHash(),
		header: &block.Header,
		block: nil,
	}
}

 */

func (self *Syncer) BlockInit(block *btcutil.Block) {
	var adr [20]byte
	if len(block.MsgBlock().Transactions[0].SignatureScripts) < 2 {
		log.Errorf("block does not contain enough signature. %d", len(block.MsgBlock().Transactions[0].SignatureScripts))
		return
	}
	if len(block.MsgBlock().Transactions[0].SignatureScripts) > wire.CommitteeSize / 2 + 1 {
		log.Infof("it is a consensus block. Skip it.")
		return
	}
	copy(adr[:], block.MsgBlock().Transactions[0].SignatureScripts[1])

	// total fees are total coinbase outputs
	fees := int64(0)
	eq := int64(-1)
	for _, txo := range block.MsgBlock().Transactions[0].TxOut {
		if txo.TokenType == 0 {
			if eq < 0 {
				eq = txo.Value.(*token.NumToken).Val
			} else if eq != txo.Value.(*token.NumToken).Val {
				return
			}
			fees += eq
		}
	}

	if len(block.MsgBlock().Transactions[0].TxOut) <= wire.CommitteeSize / 2 {
		return
	}

	self.SetCommittee()

	log.Infof("syner initialized block %s, sending to newtree", block.Hash().String())

	self.newtree <- tree {
		creator: adr,
		fees: uint64(fees),
		hash: * block.Hash(),
		header: &block.MsgBlock().Header,
		block: block,
	}
}

func (self *Syncer) makeAbout(better int32) *wire.MsgKnowledge {
	k := []int64{-1024} // indicate we are sending a map
	for _, p := range self.knowledges.Knowledge[better] {
		k = append(k, p)
	}
	t := self.forest[self.Names[better]]
	return &wire.MsgKnowledge {
		M:      t.hash,
		Height: self.Height,
		K:      k,
		Finder: t.creator,
		From:   self.Me,
		//	Signatures      map[int][]byte
	}
}

func (self *Syncer) pull(hash chainhash.Hash, from int32) {
	if _,ok := self.pulling[from]; !ok {
		// pull block
		msg := wire.MsgGetData{InvList: []*wire.InvVect{{common.InvTypeWitnessBlock, hash}}}
		log.Infof("Pull request: to %d hash %s", from+self.Base, hash.String())
		if miner.server.CommitteeMsg(self.Names[from], &msg) {
			log.Infof("Pull request sent")
			self.pulling[from] = struct{}{}
		} else {
			log.Infof("Fail to Pull !!!!!!!!")
		}
	} else {
		log.Infof("Have pulled for %d at height %d", from, self.Height)
	}
}

func (self *Syncer) Quit() {
	if self.Runnable {
		log.Info("sync quit")
		close(self.quit)
		self.wg.Wait()
	}
}

func (self *Syncer) Debug(w http.ResponseWriter, r *http.Request) {
}

func (self *Syncer) print() {
	log.Infof("Syncer for %d = %x", self.Myself, self.Me)
	log.Infof("Runnable = %d Committee = %d Base = %d", self.Runnable, self.Committee, self.Base)
	log.Infof("agreed = %d sigGiven = %d Height = %d", self.agreed, self.sigGiven, self.Height)
	log.Infof("Done = %d # of agrees = %d: %v", self.Done, len(self.agrees), self.agrees)

	knowRevd := "Knowledge received from: "
	candRevd := "Candidacy anouncement received from: "
	consRevd := "Consensus anouncement received from: "

	for i := 0; i < wire.CommitteeSize; i++ {
		knowRevd += fmt.Sprintf("%d ", self.knowRevd[i])
		candRevd += fmt.Sprintf("%d ", self.candRevd[i])
		consRevd += fmt.Sprintf("%d ", self.consRevd[i])
	}
	log.Infof("%s\n%s\n%s", knowRevd, candRevd, consRevd)

	if self.knowledges != nil {
		log.Infof("knowledges = ")

		self.knowledges.print()
	}
}

func (self *Syncer) DebugInfo() {
	log.Infof("I am %x, %d", self.Me, self.Myself)
	self.print()
	log.Infof("Members & Names:")
	for m,n := range self.Members {
		if self.Names[n] != m {
			log.Infof("Unmatched Members & Names: %x, %d", m, n)
		}
		log.Infof("Members & Names: %d, %x", n, m)
	}
	for m,n := range self.Names {
		if self.Members[n] != m {
			log.Infof("Unmatched Members & Names: %d, %x", m, n)
		}
	}

	if len(self.Malice) > 0 {
		log.Infof("Malice miners:")
		for m,_ := range self.Malice {
			log.Infof("%x", m)
		}
	}
/*
	if len(self.consents) > 0 {
		log.Infof("My consents:")
		for m,n := range self.consents {
			log.Infof("%x %d", m, n)
		}
	}

 */

	if len(self.signed) > 0 {
		log.Infof("Who has signed for this block:")
		for m,_ := range self.signed {
			log.Infof("%x", m)
		}
	}

	if len(self.pending) > 0 {
		log.Infof("Pending messages:")
		for _,q := range self.pending {
			for _,m := range q {
				log.Infof("%s", m.(wire.Message).Command())
			}
		}
	}

	log.Infof("Forrest:")
	for w,t := range self.forest {
		log.Infof("Tree of %x:", w)
		log.Infof("creator of %x:", t.creator)
		log.Infof("fees of %d:", t.fees)
		log.Infof("hash of %s:", t.hash.String())
		if t.block == nil {
			log.Infof("Tree is naked")
		}
	}
}

func (self *Syncer) better(left, right int32) bool {
	l,ok := self.forest[self.Names[left]]
	if !ok {
		return false
	}
	r,ok := self.forest[self.Names[right]]
	if !ok {
		return true
	}
	return l.fees > r.fees || (l.fees == r.fees && left > right)
}

func (self *Syncer) best() int32 {
	var seld *[20]byte
	for left,l := range self.forest {
		if seld == nil {
			seld = new([20]byte)
			copy(seld[:], left[:])
		} else {
			if l.fees > self.forest[*seld].fees ||
				(l.fees == self.forest[*seld].fees && self.Members[left] > self.Members[*seld]) {
				copy(seld[:], left[:])
			}
		}
	}
	if seld == nil {
		return -1
	}
	return self.Members[*seld]
}