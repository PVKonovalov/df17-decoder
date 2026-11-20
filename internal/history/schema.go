package history

import (
	"database/sql"
	"fmt"
	"strconv"
)

// migrations[i] brings the schema from version i to i+1.
var migrations = []string{
	`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT);

	CREATE TABLE flights (
		id INTEGER PRIMARY KEY,
		icao TEXT NOT NULL, callsign TEXT, squawk TEXT, category TEXT, country TEXT,
		adsb INTEGER NOT NULL,
		first_seen INTEGER NOT NULL,
		last_seen INTEGER NOT NULL,
		closed INTEGER NOT NULL DEFAULT 0,
		points INTEGER NOT NULL DEFAULT 0,
		max_alt INTEGER, min_distance_nm REAL
	);
	CREATE INDEX flights_time ON flights(first_seen, last_seen);
	CREATE INDEX flights_icao ON flights(icao, first_seen);

	CREATE TABLE positions (
		flight_id INTEGER NOT NULL REFERENCES flights(id),
		t INTEGER NOT NULL,
		lat REAL NOT NULL, lon REAL NOT NULL,
		alt INTEGER, alt_gnss INTEGER NOT NULL DEFAULT 0,
		gs REAL, track REAL, vr INTEGER, on_ground INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (flight_id, t)
	) WITHOUT ROWID;
	CREATE INDEX positions_t ON positions(t);

	CREATE TABLE samples (
		flight_id INTEGER NOT NULL REFERENCES flights(id),
		t INTEGER NOT NULL, alt INTEGER, squawk TEXT,
		PRIMARY KEY (flight_id, t)
	) WITHOUT ROWID;`,

	// country follows from the ICAO address (tracker.Country).
	`ALTER TABLE flights DROP COLUMN country;`,

	// hourly rollups (rollup.go), kept for stats.retention
	`CREATE TABLE hourly (
		hour INTEGER PRIMARY KEY,
		aircraft_adsb INTEGER NOT NULL,
		aircraft_modes INTEGER NOT NULL,
		flights_started INTEGER NOT NULL,
		positions INTEGER NOT NULL,
		messages INTEGER,
		max_range_nm REAL,
		max_range_bearing REAL,
		db_bytes INTEGER, tape_bytes INTEGER
	) WITHOUT ROWID;
	CREATE TABLE hourly_country (hour INTEGER NOT NULL, country TEXT NOT NULL, aircraft INTEGER NOT NULL,
		PRIMARY KEY (hour, country)) WITHOUT ROWID;
	CREATE TABLE hourly_operator (hour INTEGER NOT NULL, prefix TEXT NOT NULL, aircraft INTEGER NOT NULL,
		PRIMARY KEY (hour, prefix)) WITHOUT ROWID;`,

	// receiver health, altitude bands and notable aircraft (rollup.go); rollup_version marks hours to recompute
	`ALTER TABLE hourly ADD COLUMN frames INTEGER;
	ALTER TABLE hourly ADD COLUMN crc_failures INTEGER;
	ALTER TABLE hourly ADD COLUMN fixed_bit_errors INTEGER;
	ALTER TABLE hourly ADD COLUMN cpr_rejected INTEGER;
	ALTER TABLE hourly ADD COLUMN reconnects INTEGER;
	ALTER TABLE hourly ADD COLUMN silent_minutes INTEGER;
	ALTER TABLE hourly ADD COLUMN signal_dbfs REAL;
	ALTER TABLE hourly ADD COLUMN rollup_version INTEGER NOT NULL DEFAULT 1;
	CREATE TABLE hourly_altitude (hour INTEGER NOT NULL, band INTEGER NOT NULL, positions INTEGER NOT NULL,
		PRIMARY KEY (hour, band)) WITHOUT ROWID;
	CREATE TABLE aircraft_seen (icao TEXT PRIMARY KEY, first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL)
		WITHOUT ROWID;
	INSERT INTO aircraft_seen SELECT icao, min(first_seen), max(last_seen) FROM flights GROUP BY icao;
	CREATE TABLE notable_events (kind TEXT NOT NULL, icao TEXT NOT NULL, flight_id INTEGER NOT NULL,
		squawk TEXT NOT NULL DEFAULT '', t INTEGER NOT NULL, callsign TEXT,
		PRIMARY KEY (kind, icao, flight_id, squawk)) WITHOUT ROWID;
	CREATE INDEX notable_t ON notable_events(t);`,
}

// SchemaVersion is the schema version this build writes.
var SchemaVersion = len(migrations)

// schemaVersion returns the version stored in meta, 0 for an empty database.
func schemaVersion(db *sql.DB) (int, error) {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'`).Scan(&n); err != nil || n == 0 {
		return 0, err
	}
	var v string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v); err != nil {
		return 0, fmt.Errorf("history: schema version: %w", err)
	}
	return strconv.Atoi(v)
}

// migrate applies the missing migrations, each in its own transaction, and returns the versions before and after.
func migrate(db *sql.DB) (from, to int, err error) {
	from, err = schemaVersion(db)
	if err != nil {
		return 0, 0, err
	}
	if from > len(migrations) {
		return from, from, fmt.Errorf("history: database schema version %d is newer than this build (%d)", from, len(migrations))
	}
	for v := from; v < len(migrations); v++ {
		tx, err := db.Begin()
		if err != nil {
			return from, v, err
		}
		if _, err = tx.Exec(migrations[v]); err == nil {
			_, err = tx.Exec(`INSERT INTO meta(key, value) VALUES('schema_version', ?)
				ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.Itoa(v+1))
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			return from, v, fmt.Errorf("history: migration %d: %w", v+1, err)
		}
	}
	return from, len(migrations), nil
}
