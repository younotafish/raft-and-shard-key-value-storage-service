# MIT6.824 Lab

## MapReduce (in src/mr)

1. This part is the implementation of google paper [**MapReduce: Simplified Data Processing on Large Clusters**](https://pdos.csail.mit.edu/6.824/papers/mapreduce.pdf)
2. MapReduce is a classic distributed system paradigm. In the system, developers don't need to handle distributed dirty parts and can write regular (Map/Reduce) functions instead.
3. The code I write for the system is in `src/mr`. The overall function is:
4. the Leader/Master will first distribute all the tasks to different followers. The followers can then handle the tasks using the Map functions.
5. After all the tasks are done, a leader can then assign the mapped tasks to the same followers and let them do the final reduce work with the regular reduce function provided.
6. The difficulties in the code are:
    1.  how to dynamically coordinate the followers as some followers might fail down and some other followers might join during the process.  

## Raft(in src/raft)

1. This part is the implementation of classic paper: [Raft](https://pdos.csail.mit.edu/6.824/papers/raft-extended.pdf)

2. Raft contains three major modules:

    1. Leader election: elect a leader and the system should work even if a minority of machines don't work
    2. Append entries: leader append entries to all other peers
    3. Snapshot: once get maximum entries, do a snapshot.

3. The logic behind this is:

    1. - When making a raft, the for loop inside the go program is opened indefinitely to detect changes in chan and react. For example, a follower detects a heartbeat and is ready to become a candidate at any time; the leader sends a heartbeat; the candidate sends the vote to check whether it becomes the leader

    - Each term is initiated by election. Initiated by the candidate, use RPC to synchronize other raft terms

    - appendEntries, only send the log after the prev Index. If it is empty, then it is a heartbeat

    - All servers need to commit. When the follower detects that the leader has committed, he will commit. 1. Modify the state machine and apply to the server to modify the parameters and maintain synchronization. The databases of the members of the group need to be kept in sync. If the leader is down, become leader 2. When 1/2 of the follower replica becomes. The leader will commit, and only commit the current term



# Shard Key/Value Store Service (in src/kvraft, src/shardmaster, src/shardkv)

1. Build a key/value store service on top of Raft and the mechanism is much like (Spanner)(https://pdos.csail.mit.edu/6.824/papers/spanner.pdf)

2. The system contains three parts:

    1. A fault-tolerant key/value storage service with strong consistency:
        1. The service supports three operations: Put(key,value), Get(key), Append(key,arg).
        2. The service supports snapshots service allows Raft to discard old log entries.
    2. A master service handling re-config by by `Join` in many other machines, `Leave` a subset of machines, `Move` a subset of the machine to other groups.
    3. Shard the data into different groups and rebalance the system if some group is more loaded than others.

3. The logic behind this is:

    1. Kv server process sequence: clerk sends the request -> server accepts, calls start to add op to log -> raft learned that the leader adjusts the synchronization problem and then starts appendEntries when committing (applyCh has content). -> The server senses it and starts to process the content. -> server completes the task and replies to the clerk

    2. - Server logic: The server detects that the persist content of raft is too long, StartSnapShot shortens the log itself

    - Initialization logic: a raft is initialized -> get snapshot data of persisting readSnapShot -> 1 check log whether it needs to be truncated. 2 chanApply adds special msg -> server-side decode to get index, term, DB, ack, and load the last persist data

    - The leader's synchronization logic for the follower: starting from appendEntries, if your own base is larger than others' next, it means that you have log compacted, then use the snapshot to synchronize -> the receiver installs snap and modifies your log. chanApply adds special msg updates At the same time, the reply also involves the degeneration of the leader and the function of updating the nextIndex of the follower.

    3. - The server receives the raft message and then parses msg to obtain op. Then do different processing according to different businesses. For example, put and move to require operations (lab4 requires operations). get only needs to add reply outside the main coroutine

    - Join requires each group to join the server. If it is a group that did not exist before, you need to readjust the slices of each group, and you need to take from the most groups until the difference with the most groups is less than or equal to 1.

    - The situation of raft, such as the allocation of shards and the group itself is not forced, all rely on config to record. This is the same as the previous k-v and log

    - Summary In lab4a, the master is a shards management system, and it needs to ensure disaster tolerance. Responsible for managing the groups and responsible shards of the system to ensure load balance

    4. Handle disaster recovery in each group and realize the basic functions of the kv server. When the master adjusts different groups and corresponding shards, kvshard sends shards to other groups through RPC. The synchronization problem of members in the group is similar to that of kvRaft.

        - The servers in a group are required to synchronize the migration data, because the server itself has a shard, which is part of the database.

        - When the config changes, the server needs to deal with the newly owned shards.

        - Communication between master and kvShard: kvShard regularly obtains na ew config from the master client and then adjusts it. The master server itself receives the clerk command to modify the config, which is