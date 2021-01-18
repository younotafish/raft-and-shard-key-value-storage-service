package raft

import (
	"time"
)

type AppendEntriesArgs struct {
	Term         int
	LeaderId     int
	PrevLogIndex int
	PervLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term      int
	Success   bool
	NextIndex int
}

func (rf *Raft) getNextIndex() int {
	_, idx := rf.lastLogTermIndex()
	return idx + 1
}


/*
prevLogIndex is the index of log entry immediately preceding the first of new ones
*/
func (rf *Raft) outOfOrderAppendEntries(args *AppendEntriesArgs) bool {
	// prevlog 已经对的上
	argsLastIndex := args.PrevLogIndex + len(args.Entries)
	lastTerm, lastIndex := rf.lastLogTermIndex()
	if argsLastIndex < lastIndex && lastTerm == args.Term {
		return true
	}
	return false
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.lock("append_entries")
	rf.log("get appendentries:%+v", *args)
	reply.Term = rf.term

	if rf.term > args.Term {
		rf.unlock("append_entries")
		return
	}

	rf.term = args.Term
	rf.changeRole(Follower)
	rf.resetElectionTimer()
	_, lastLogIndex := rf.lastLogTermIndex()

	if args.PrevLogIndex < rf.lastSnapshotIndex {

		reply.Success = false
		reply.NextIndex = rf.lastSnapshotIndex + 1
	} else if args.PrevLogIndex > lastLogIndex {
		// lack some logs in between
		reply.Success = false
		reply.NextIndex = rf.getNextIndex()
	} else if args.PrevLogIndex == rf.lastSnapshotIndex {
		// TODO 重复代码
		// 上一个刚好是快照
		if rf.outOfOrderAppendEntries(args) {
			reply.Success = false
			reply.NextIndex = 0
		} else {
			reply.Success = true
			rf.logEntries = append(rf.logEntries[:1], args.Entries...) // keep logs[0]
			reply.NextIndex = rf.getNextIndex()
		}
	} else if rf.logEntries[rf.getRealIdxByLogIndex(args.PrevLogIndex)].Term == args.PervLogTerm {
		// the logic here is, the appendEntries is a out-of-date rpc and we should just ignore it.
		if rf.outOfOrderAppendEntries(args) {
			reply.Success = false
			reply.NextIndex = 0
		} else {
			reply.Success = true
			rf.logEntries = append(rf.logEntries[0:rf.getRealIdxByLogIndex(args.PrevLogIndex)+1], args.Entries...)
			reply.NextIndex = rf.getNextIndex()
		}
	} else {
		rf.log("prev log not match")
		reply.Success = false
		// 尝试跳过一个 term
		term := rf.logEntries[rf.getRealIdxByLogIndex(args.PrevLogIndex)].Term
		idx := args.PrevLogIndex
		// idx is the minimum index of current term
		for idx > rf.commitIndex && idx > rf.lastSnapshotIndex && rf.logEntries[rf.getRealIdxByLogIndex(idx)].Term == term {
			idx -= 1
		}
		reply.NextIndex = idx + 1
	}
	if reply.Success {
		if rf.commitIndex < args.LeaderCommit {
			rf.commitIndex = args.LeaderCommit
			rf.notifyApplyCh <- struct{}{}
		}
	}

	rf.persist()
	rf.log("get appendentries:%+v, reply:%+v", *args, *reply)
	rf.unlock("append_entries")
}

func (rf *Raft) getAppendLogs(peerIdx int) (prevLogIndex, prevLogTerm int, res []LogEntry) {
	nextIdx := rf.nextIndex[peerIdx]
	lastLogTerm, lastLogIndex := rf.lastLogTermIndex()
	if nextIdx <= rf.lastSnapshotIndex || nextIdx > lastLogIndex {
		// 没有需要发送的 log
		prevLogIndex = lastLogIndex
		prevLogTerm = lastLogTerm
		return
	}

	res = append([]LogEntry{}, rf.logEntries[rf.getRealIdxByLogIndex(nextIdx):]...)
	prevLogIndex = nextIdx - 1
	if prevLogIndex == rf.lastSnapshotIndex {
		prevLogTerm = rf.lastSnapshotTerm
	} else {
		prevLogTerm = rf.getLogByIndex(prevLogIndex).Term
	}
	return
}

func (rf *Raft) getAppendEntriesArgs(peerIdx int) AppendEntriesArgs {
	prevLogIndex, prevLogTerm, logs := rf.getAppendLogs(peerIdx)
	args := AppendEntriesArgs{
		Term:         rf.term,
		LeaderId:     rf.me,
		PrevLogIndex: prevLogIndex,
		PervLogTerm:  prevLogTerm,
		Entries:      logs,
		LeaderCommit: rf.commitIndex,
	}
	return args
}

func (rf *Raft) resetHeartBeatTimers() {
	for i, _ := range rf.appendEntriesTimers {
		//rf.appendEntriesTimers[i].Stop()
		rf.appendEntriesTimers[i].Reset(0)
	}
}

func (rf *Raft) resetHeartBeatTimer(peerIdx int) {
	rf.appendEntriesTimers[peerIdx].Stop()
	rf.appendEntriesTimers[peerIdx].Reset(HeartBeatTimeout)
}

func (rf *Raft) appendEntriesToPeer(peerIdx int) {
	RPCTimer := time.NewTimer(RPCTimeout)
	defer RPCTimer.Stop()

	for !rf.killed() {
		rf.lock("appendtopeer1")
		if rf.role != Leader {
			rf.resetHeartBeatTimer(peerIdx)
			rf.unlock("appendtopeer1")
			return
		}
		args := rf.getAppendEntriesArgs(peerIdx)
		rf.resetHeartBeatTimer(peerIdx)
		rf.unlock("appendtopeer1")

		RPCTimer.Stop()
		RPCTimer.Reset(RPCTimeout)
		reply := AppendEntriesReply{}
		resCh := make(chan bool, 1)
		go func(args *AppendEntriesArgs, reply *AppendEntriesReply) {
			ok := rf.peers[peerIdx].Call("Raft.AppendEntries", args, reply)
			if !ok {
				time.Sleep(time.Millisecond * 10)
			}
			resCh <- ok
		}(&args, &reply)

		select {
		case <-rf.stopCh:
			return
		case <-RPCTimer.C:
			rf.log("appendtopeer, rpctimeout: peer:%d, args:%+v", peerIdx, args)
			continue
		case ok := <-resCh:
			if !ok {
				rf.log("appendtopeer not ok")
				continue
			}
		}

		rf.log("appendtoperr, peer:%d, args:%+v, reply:%+v", peerIdx, args, reply)
		// call ok, check reply
		rf.lock("appendtopeer2")
		if reply.Term > rf.term {
			rf.changeRole(Follower)
			rf.resetElectionTimer()
			rf.term = reply.Term
			rf.persist()
			rf.unlock("appendtopeer2")
			return
		}
		// quick fail
		if rf.role != Leader || rf.term != args.Term {
			rf.unlock("appendtopeer2")
			return
		}

		if reply.Success {
			if reply.NextIndex > rf.nextIndex[peerIdx] {
				rf.nextIndex[peerIdx] = reply.NextIndex
				rf.matchIndex[peerIdx] = reply.NextIndex - 1
			}
			if len(args.Entries) > 0 && args.Entries[len(args.Entries)-1].Term == rf.term {
				// each success reply can trigger a commit
				rf.updateCommitIndex()
			}
			rf.persist()
			rf.unlock("appendtopeer2")
			return
		}

		// success == false
		if reply.NextIndex != 0 {
			if reply.NextIndex > rf.lastSnapshotIndex {
				rf.nextIndex[peerIdx] = reply.NextIndex
				rf.unlock("appendtopeer2")
				continue
				// need retry
			} else {
				// send sn rpc
				go rf.sendInstallSnapshot(peerIdx)
				rf.unlock("appendtopeer2")
				return
			}
		} else {
			// the append rpc just sent is out of order, just ignore it
			rf.unlock("appendtopeer2")
		}
	}

}

func (rf *Raft) updateCommitIndex() {
	rf.log("in update commitindex")
	hasCommit := false
	for i := rf.commitIndex + 1; i <= rf.lastSnapshotIndex+len(rf.logEntries); i++ {
		count := 0
		for _, m := range rf.matchIndex {
			if m >= i {
				count += 1
				if count > len(rf.peers)/2 {
					rf.commitIndex = i
					hasCommit = true
					rf.log("update commit index:%d", i)
					break
				}
			}
		}
		if rf.commitIndex != i {
			// 后续的不需要再判断
			break
		}
	}
	if hasCommit {
		rf.notifyApplyCh <- struct{}{}
	}
}
