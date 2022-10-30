package kfx

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	// "strings"
	"time"

	"github.com/amzn/ion-go/ion"
	"go.uber.org/zap"
	_ "modernc.org/sqlite"

	"fb2converter/archive"
	"fb2converter/state"
)

const (
	DirKdf = "KDF"
)

type eid int64

// Default values.
const (
	defaultCompression     = 0
	defaultDRMScheme       = 0
	defaultChunkSize       = 4096
	defaultFragmentVersion = 1
)

type frag struct {
	version     int
	compression int
	drm         int
	ftype       ion.SymbolToken
	fid         ion.SymbolToken
	data        []byte
}

func newFrag(ftype, fid ion.SymbolToken, data []byte) *frag {
	return &frag{
		version:     defaultFragmentVersion,
		compression: defaultCompression,
		drm:         defaultDRMScheme,
		fid:         fid,
		ftype:       ftype,
		data:        data,
	}
}

type cnvrtr struct {
	log *zap.Logger
	//
	book         *sql.DB
	tables       map[string]struct{} // resulting set of tables to work with
	eidSymbols   map[eid]ion.SymbolToken
	elementTypes map[string]string
	fragments    []*frag
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

func (c *cnvrtr) openDB(sqlFile string) (err error) {

	c.book, err = sql.Open("sqlite", sqlFile)
	if err != nil {
		return err
	}
	return nil
}

func (c *cnvrtr) closeDB() {

	if c.book != nil {
		if err := c.book.Close(); err != nil {
			c.log.Warn("Unable to close database cleanly", zap.Error(err))
		}
	}
}

// Check book database schema sinse Amazon is known to change it at will.
// Make sure that all necessary tables exist and have proper structure and that book does not have unexpected tables.
// Return set of all known table names found or error.
func (c *cnvrtr) readSchema() error {

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

	c.tables = make(map[string]struct{})

	rows, err := c.book.Query("SELECT name, sql FROM sqlite_master WHERE type='table';")
	if err != nil {
		return fmt.Errorf("unable to get database tables: %w", err)
	}

	for rows.Next() {
		var tbl, schema string
		if err := rows.Scan(&tbl, &schema); err != nil {
			return fmt.Errorf("unable to scan next row: %w", err)
		}
		if name, found := knowns[schema]; !found {
			return fmt.Errorf("unexpected database table %s[%s]", tbl, schema)
		} else if name != tbl {
			return fmt.Errorf("unexpected database table name %s for [%s]", tbl, schema)
		}
		if _, found := mustHave[tbl]; found {
			delete(mustHave, tbl)
		}
		c.tables[tbl] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("unable to iterate on rows: %w", err)
	}

	if len(mustHave) > 0 {
		var absent string
		for k := range mustHave {
			absent += " " + k
		}
		return fmt.Errorf("unable to find some of expected tables: %s", absent)
	}
	return nil
}

func (c *cnvrtr) readKfxIDTranslations() error {

	// optional table
	if _, found := c.tables["kfxid_translation"]; !found {
		return nil
	}
	c.eidSymbols = make(map[eid]ion.SymbolToken)

	rows, err := c.book.Query("SELECT eid, kfxid FROM kfxid_translation;")
	if err != nil {
		return fmt.Errorf("unable to execute query on kfxid_translation table: %w", err)
	}
	for rows.Next() {
		var (
			eid   eid
			kfxid string
		)
		if err := rows.Scan(&eid, &kfxid); err != nil {
			return fmt.Errorf("unable to scan to next row on kfxid_translation table: %w", err)
		}
		c.eidSymbols[eid] = createLocalSymbolToken(kfxid, c.log)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("unable to iterate on kfxid_translation table rows: %w", err)
	}
	return nil
}

func (c *cnvrtr) readFragmentProperties() error {

	// optional table
	if _, found := c.tables["fragment_properties"]; !found {
		return nil
	}
	c.elementTypes = make(map[string]string)

	rows, err := c.book.Query("SELECT id, key, value FROM fragment_properties;")
	if err != nil {
		return fmt.Errorf("unable to execute query on fragment_properties table: %w", err)
	}
	for rows.Next() {
		var id, key, value string
		if err := rows.Scan(&id, &key, &value); err != nil {
			return fmt.Errorf("unable to scan to next row on fragment_properties table: %w", err)
		}
		switch key {
		case "child":
		case "element_type":
			c.elementTypes[id] = value
		default:
			return fmt.Errorf("fragment property has unknown key: %s (%s:%s)", key, id, value)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("unable to iterate on fragment_properties table rows: %w", err)
	}
	return nil
}

func (c *cnvrtr) readFragments() error {

	c.fragments = make([]*frag, 0, 128)

	// Get symbol table

	var ist []byte
	if err := c.book.QueryRow("SELECT payload_value FROM fragments WHERE id = '$ion_symbol_table' AND payload_type = 'blob';").Scan(&ist); err != nil {
		return fmt.Errorf("unable to query $ion_symbol_table fragment: %w", err)
	}
	rdr := ion.NewReaderBytes(ist)
	if val, err := ion.NewDecoder(rdr).Decode(); err != nil && !errors.Is(err, ion.ErrNoInput) {
		return fmt.Errorf("unable to decode KDF $ion_symbol_table fragment: %w", err)
	} else if val != nil {
		return fmt.Errorf("unexpected value %+v for KDF $ion_symbol_table fragment ", val)
	}
	if len(rdr.SymbolTable().Imports()) != 2 {
		return fmt.Errorf("unexpected number of imports %d for KDF $ion_symbol_table fragment: %s", len(rdr.SymbolTable().Imports()), rdr.SymbolTable().String())
	}
	if rdr.SymbolTable().Imports()[0].Name() != "$ion" || rdr.SymbolTable().Imports()[1].Name() != "YJ_symbols" {
		return fmt.Errorf("unexpected import for KDF $ion_symbol_table fragment: %s", rdr.SymbolTable().String())
	}

	// Check consistency - verify provided symbol table size

	var (
		maxID uint64
		blob  []byte
	)
	if err := c.book.QueryRow("SELECT payload_value FROM fragments WHERE id = 'max_id' AND payload_type = 'blob';").Scan(&blob); err != nil {
		return fmt.Errorf("unable to query max_id fragment: %w", err)
	}
	if err := ion.NewDecoder(ion.NewReaderBytes(blob)).DecodeTo(&maxID); err != nil {
		if !errors.Is(err, ion.ErrNoInput) {
			return fmt.Errorf("unable to decode KDF max_id fragment: %w", err)
		}
		if maxID == 0 {
			return errors.New("unexpected value in KDF max_id fragment: <nil>")
		}
	}
	if maxID != rdr.SymbolTable().MaxID() {
		return fmt.Errorf("max_id (%d) in KDF max_id fragment is is not equial to number of symbols in KDF $ion_symbol_table fragment (%d)", maxID, rdr.SymbolTable().MaxID())
	}

	c.log.Debug("Symbol_table", zap.Stringer("$ion_symbol_table", rdr.SymbolTable()))

	// Process payload
	/*
		// sstYJ := createSST(rdr.SymbolTable().Imports()[1].Name(), rdr.SymbolTable().Imports()[1].Version(), rdr.SymbolTable().Imports()[1].MaxID())
		stb := ion.NewSymbolTableBuilder(nil)

		rows, err := c.book.Query("SELECT id, payload_type, payload_value FROM fragments WHERE id != 'max_id' and id != '$ion_symbol_table';")
		if err != nil {
			return fmt.Errorf("unable to execute payload query: %w", err)
		}
		for rows.Next() {
			var id, ptype string
			if err := rows.Scan(&id, &ptype, &blob); err != nil {
				return fmt.Errorf("unable to scan for next row on fragments table: %w", err)
			}
			switch ptype {
			case "blob":

				if len(blob) == 0 {
					ftype, _ := c.elementTypes[id]
					c.log.Debug("Empty KDF fragment (data is empty), ignoring...", zap.String("id", id), zap.String("type", ptype), zap.String("ftype", ftype))
					continue
				}

				// Normally this does not happen - there is "path" record for that
				if !bytes.HasPrefix(blob, ionBVM) {
					if !strings.HasPrefix(id, "resource/") {
						id = fmt.Sprintf("resource/%s", id)
					}
					frag, err := newFragment(createSymbolToken(stb, "$417", log), createSymbolToken(stb, id, log), blob)
					if err != nil {
						return frags, fmt.Errorf("unable to create path fragment id:(%s):payload_type(%s): %w", id, ptype, err)
					}
					frags = append(frags, frag)
					continue
				}

				if bytes.Equal(blob, ionBVM) {
					if id != "book_navigation" {
						log.Warn("Empty KDF fragment (BVM only), ignoring...", zap.String("id", id), zap.String("type", ptype))
					}
					continue
				}

				r := ion.NewReaderCat(io.MultiReader(bytes.NewReader(ist), bytes.NewReader(blob[len(ionBVM):])), ion.NewCatalog(sstYJ))
				if !r.Next() {
					if r.Err() != nil {
						return frags, fmt.Errorf("unable to read value annotations for KDF fragment %s: %w", id, r.Err())
					}
					return frags, fmt.Errorf("unable to read value annotations for KDF fragment %s: empty value", id)
				}
				annots, err := r.Annotations()
				if err != nil {
					return frags, fmt.Errorf("unable to read value annotations for KDF fragment %s: %w", id, err)
				}

				switch l := len(annots); {
				case l == 0:
					log.Error("KDF fragment must have annotation, skipping...", zap.String("id", id))
					continue
				case l == 2 && *annots[1].Text == "$608":
				case l > 1:
					log.Error("KDF fragment should have single annotation, ignoring...", zap.String("id", id), zap.Int("count", l))
					continue
				}
				if r.Type() == ion.NoType {
					log.Error("KDF fragment cannot be empty, ignoring...", zap.String("id", id))
					continue
				}
				data, err := dereferenceKfxIDs(r, stb, eids, log)
				if err != nil {
					return frags, fmt.Errorf("unable to dereference KDF fragment %s: %w", id, err)
				}
				frag, err := newFragment(createSymbolToken(stb, *annots[0].Text, log), createSymbolToken(stb, id, log), data)
				if err != nil {
					return frags, fmt.Errorf("unable to create dereferenced fragment id:(%s,%s):payload_type(%s): %w", *annots[0].Text, id, ptype, err)
				}
				frags = append(frags, frag)

			case "path":
				if !strings.HasPrefix(id, "resource/") {
					id = fmt.Sprintf("resource/%s", id)
				}
				frag, err := newFragment(createSymbolToken(stb, "$417", log), createSymbolToken(stb, id, log), blob)
				if err != nil {
					return frags, fmt.Errorf("unable to create path fragment id:(%s):payload_type(%s): %w", id, ptype, err)
				}
				frags = append(frags, frag)

			default:
				return frags, fmt.Errorf("unexpected KDF fragment type (%s) with id (%s) size %d", ptype, id, len(blob))
			}

		}
		if err := rows.Err(); err != nil {
			return frags, fmt.Errorf("unable to iterate on rows: %w", err)
		}
		return frags, nil
	*/
	return nil
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

	if err := c.openDB(sqlFile); err != nil {
		return fmt.Errorf("unable to open sqlite3 database (%s): %w", sqlFile, err)

	}
	defer c.closeDB()

	if err := c.readSchema(); err != nil {
		return fmt.Errorf("bad book database, possibly new kindle previewer was installed recently: %w", err)
	}

	if err := c.readKfxIDTranslations(); err != nil {
		return fmt.Errorf("bad book database: %w", err)
	}

	if err := c.readFragmentProperties(); err != nil {
		return fmt.Errorf("bad book database: %w", err)
	}

	if err := c.readFragments(); err != nil {
		return fmt.Errorf("bad book database: %w", err)
	}

	// env.Log.Debug("Done", zap.Int("len", len(c.props)), zap.Any("eids", c.props))

	return fmt.Errorf("FIX ME DONE: ConvertFromKpf")
}
