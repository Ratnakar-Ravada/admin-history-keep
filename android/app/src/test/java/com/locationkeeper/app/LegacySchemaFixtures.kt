package com.locationkeeper.app

import android.database.sqlite.SQLiteDatabase

/** Build an actual pre-v5 fixture without relying on SQLite DROP COLUMN support. */
fun recreateLegacyLocationTable(db: SQLiteDatabase) {
    val ddl=db.rawQuery("SELECT sql FROM sqlite_master WHERE type='table' AND name='locations'",null).use {
        check(it.moveToFirst());it.getString(0)
    }.replace(Regex(""",\s*airplane_mode_enabled\s+INTEGER"""),"")
    val columns=db.rawQuery("PRAGMA table_info(locations)",null).use {
        buildList {while(it.moveToNext()) {val name=it.getString(1);if(name!="airplane_mode_enabled")add(name)}}
    }.joinToString(",")
    db.beginTransaction()
    try {
        db.execSQL("ALTER TABLE locations RENAME TO locations_fixture_new")
        db.execSQL("DROP INDEX IF EXISTS locations_pending")
        db.execSQL(ddl)
        db.execSQL("INSERT INTO locations($columns) SELECT $columns FROM locations_fixture_new")
        db.execSQL("DROP TABLE locations_fixture_new")
        db.execSQL("CREATE INDEX locations_pending ON locations(received_at,recorded_at_ms)")
        db.setTransactionSuccessful()
    } finally {db.endTransaction()}
}
