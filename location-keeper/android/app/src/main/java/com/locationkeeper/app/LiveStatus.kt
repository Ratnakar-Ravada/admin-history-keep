package com.locationkeeper.app

/** Latest known server confirmation, never a heartbeat or a promise of continuous GPS. */
object LiveStatus {
    fun isLive(snapshot: SyncSnapshot,networkAvailable: Boolean,now: Long): Boolean =
        networkAvailable && snapshot.confirmed && snapshot.successMs>0 &&
            now-snapshot.successMs in 0..90*60_000L
}
