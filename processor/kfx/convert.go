package kfx

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	_ "modernc.org/sqlite"

	"fb2converter/archive"
	"fb2converter/state"
)

const (
	DirKdf = "KDF"
)

type cnvrtr struct {
	log *zap.Logger
}

// unpacking KPF which is zipped KDF.
func (c *cnvrtr) unpackKpf(kpf, kdf string) error {

	if err := os.MkdirAll(kdf, 0700); err != nil {
		return fmt.Errorf("unable to create directories for KDF contaner: %w", err)
	}
	if err := archive.Unzip(kpf, kdf); err != nil {
		return fmt.Errorf("unable to unzip KDF contaner (%s): %w", kpf, err)
	}
	return nil
}

// unscrambing book.kdf which is scrambled sqlite3 database.
func (c *cnvrtr) unwrapKdf(kdfBook, sqlFile string) error {

	const (
		wrapperOffset      = 0x400
		wrapperLength      = 0x400
		wrapperFrameLength = 0x100000
	)

	var (
		err         error
		data        []byte
		signature   = []byte("SQLite format 3\x00")
		fingerprint = []byte("\xfa\x50\x0a\x5f")
		header      = []byte("\x01\x00\x00\x40\x20")
	)

	if data, err = os.ReadFile(kdfBook); err != nil {
		return err
	}
	if len(data) <= len(signature) || len(data) < 2*wrapperOffset {
		return fmt.Errorf("unexpected SQLite file length: %d", len(data))
	}
	if !bytes.Equal(signature, data[:len(signature)]) {
		return fmt.Errorf("unexpected SQLite file signature: %v", data[:len(signature)])
	}

	unwrapped := make([]byte, 0, len(data))
	prev, curr := 0, wrapperOffset
	for ; curr+wrapperLength <= len(data); prev, curr = curr+wrapperLength, curr+wrapperLength+wrapperFrameLength {
		if !bytes.Equal(fingerprint, data[curr:curr+len(fingerprint)]) {
			return fmt.Errorf("unexpected fingerprint: %v", data[curr:curr+len(fingerprint)])
		}
		if !bytes.Equal(header, data[curr+len(fingerprint):curr+len(fingerprint)+len(header)]) {
			return fmt.Errorf("unexpected fingerprint header: %v", data[curr+len(fingerprint):curr+len(fingerprint)+len(header)])
		}
		unwrapped = append(unwrapped, data[prev:curr]...)
	}
	unwrapped = append(unwrapped, data[prev:]...)

	if err = os.WriteFile(sqlFile, unwrapped, 0600); err != nil {
		return err
	}
	return nil
}

// Check book database schema sinse Amazon is known to change it at will.
// Make sure that all necessary tables exist and have proper structure and that book does not have unexpected tables.
// Return set of all known table names found or error.
func readSchema(db *sql.DB) (map[string]struct{}, error) {

	// those are the ones we know about
	var knowns = map[string]string{
		"CREATE TABLE index_info(namespace char(256), index_name char(256), property char(40), primary key (namespace, index_name)) without rowid": "index_info",
		"CREATE TABLE kfxid_translation(eid INTEGER, kfxid char(40), primary key(eid)) without rowid":                                              "kfxid_translation",
		"CREATE TABLE fragment_properties(id char(40), key char(40), value char(40), primary key (id, key, value)) without rowid":                  "fragment_properties",
		"CREATE TABLE fragments(id char(40), payload_type char(10), payload_value blob, primary key (id))":                                         "fragments",
		"CREATE TABLE gc_fragment_properties(id varchar(40), key varchar(40), value varchar(40), primary key (id, key, value)) without rowid":      "gc_fragment_properties",
		"CREATE TABLE gc_reachable(id varchar(40), primary key (id)) without rowid":                                                                "gc_reachable",
		"CREATE TABLE capabilities(key char(20), version smallint, primary key (key, version)) without rowid":                                      "capabilities",
	}

	var mustHave = map[string]struct{}{
		"capabilities": {},
		"fragments":    {},
	}

	names := make(map[string]struct{})

	rows, err := db.Query("SELECT name, sql FROM sqlite_master WHERE type='table';")
	if err != nil {
		return nil, fmt.Errorf("unable to get database tables: %w", err)
	}

	for rows.Next() {
		var tbl, schema string
		if err := rows.Scan(&tbl, &schema); err != nil {
			return nil, fmt.Errorf("unable to scan next row: %w", err)
		}
		if name, found := knowns[schema]; !found {
			return nil, fmt.Errorf("unexpected database table %s[%s]", tbl, schema)
		} else if name != tbl {
			return nil, fmt.Errorf("unexpected database table name %s for [%s]", tbl, schema)
		}
		if _, found := mustHave[tbl]; found {
			delete(mustHave, tbl)
		}
		names[tbl] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("unable to iterate on rows: %w", err)
	}

	if len(mustHave) > 0 {
		var absent string
		for k := range mustHave {
			absent += " " + k
		}
		return nil, fmt.Errorf("unable to find some of expected tables: %s", absent)
	}
	return names, nil
}

// ConvertFromKpf() takes KPT file and re-packs it to KFX file sutable for Kindle.
func ConvertFromKpf(fromKpf, toKfx, outDir string, env *state.LocalEnv) error {

	start := time.Now()
	env.Log.Debug("Repacking to KFX - start")
	defer func(start time.Time) {
		env.Log.Debug("Repacking to KFX - done",
			zap.Duration("elapsed", time.Since(start)),
			zap.String("from", fromKpf),
			zap.String("to", toKfx),
		)
	}(start)

	c := cnvrtr{
		log: env.Log,
	}

	kdfDir := filepath.Join(outDir, DirKdf)
	if err := c.unpackKpf(fromKpf, kdfDir); err != nil {
		return err
	}

	kdfBook := filepath.Join(kdfDir, "resources", "book.kdf")
	sqlFile := filepath.Join(kdfDir, "book.sqlite")
	if err := c.unwrapKdf(kdfBook, sqlFile); err != nil {
		return err
	}

	book, err := sql.Open("sqlite", sqlFile)
	if err != nil {
		return fmt.Errorf("unable to open sqlite3 database (%s): %w", sqlFile, err)
	}
	defer func() {
		if err := book.Close(); err != nil {
			env.Log.Warn("Unable to close database cleanly", zap.String("db", sqlFile), zap.Error(err))
		}
	}()

	tables, err := readSchema(book)
	if err != nil {
		return fmt.Errorf("bad book database, possibly new kindle previever was installe recently: %w", err)
	}
	env.Log.Debug("Schema read", zap.Any("tables", tables))

	return fmt.Errorf("FIX ME DONE: ConvertFromKpf")
}
