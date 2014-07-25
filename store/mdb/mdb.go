package mdb

import (
	mdb "github.com/influxdb/gomdb"
	"github.com/siddontang/ledisdb/store/driver"
	"os"
)

type Config struct {
	Path    string
	MapSize int
}

type MDB struct {
	env  *mdb.Env
	db   mdb.DBI
	path string
}

func Open(c *Config) (MDB, error) {
	path := c.Path
	if c.MapSize == 0 {
		c.MapSize = 1024 * 1024 * 1024
	}

	env, err := mdb.NewEnv()
	if err != nil {
		return MDB{}, err
	}

	// TODO: max dbs should be configurable
	if err := env.SetMaxDBs(1); err != nil {
		return MDB{}, err
	}
	if err := env.SetMapSize(uint64(c.MapSize)); err != nil {
		return MDB{}, err
	}

	if _, err := os.Stat(path); err != nil {
		err = os.MkdirAll(path, 0755)
		if err != nil {
			return MDB{}, err
		}
	}

	err = env.Open(path, mdb.NOSYNC|mdb.NOMETASYNC|mdb.WRITEMAP|mdb.MAPASYNC|mdb.CREATE, 0755)
	if err != nil {
		return MDB{}, err
	}

	tx, err := env.BeginTxn(nil, 0)
	if err != nil {
		return MDB{}, err
	}

	dbi, err := tx.DBIOpen(nil, mdb.CREATE)
	if err != nil {
		return MDB{}, err
	}

	if err := tx.Commit(); err != nil {
		return MDB{}, err
	}

	db := MDB{
		env:  env,
		db:   dbi,
		path: path,
	}

	return db, nil
}

func Repair(c *Config) error {
	println("llmd not supports repair")
	return nil
}

func (db MDB) Put(key, value []byte) error {
	itr := db.iterator(false)
	defer itr.Close()

	itr.err = itr.c.Put(key, value, 0)
	itr.setState()
	return itr.Error()
}

func (db MDB) BatchPut(writes []Write) error {
	itr := db.iterator(false)

	for _, w := range writes {
		if w.Value == nil {
			itr.key, itr.value, itr.err = itr.c.Get(w.Key, mdb.SET)
			if itr.err == nil {
				itr.err = itr.c.Del(0)
			}
		} else {
			itr.err = itr.c.Put(w.Key, w.Value, 0)
		}

		if itr.err != nil && itr.err != mdb.NotFound {
			break
		}
	}
	itr.setState()

	return itr.Close()
}

func (db MDB) Get(key []byte) ([]byte, error) {
	tx, err := db.env.BeginTxn(nil, mdb.RDONLY)
	if err != nil {
		return nil, err
	}
	defer tx.Commit()

	v, err := tx.Get(db.db, key)
	if err == mdb.NotFound {
		return nil, nil
	}
	return v, err
}

func (db MDB) Delete(key []byte) error {
	itr := db.iterator(false)
	defer itr.Close()

	itr.key, itr.value, itr.err = itr.c.Get(key, mdb.SET)
	if itr.err == nil {
		itr.err = itr.c.Del(0)
	}
	itr.setState()
	return itr.Error()
}

type MDBIterator struct {
	key   []byte
	value []byte
	c     *mdb.Cursor
	tx    *mdb.Txn
	valid bool
	err   error
}

func (itr *MDBIterator) Key() []byte {
	return itr.key
}

func (itr *MDBIterator) Value() []byte {
	return itr.value
}

func (itr *MDBIterator) Valid() bool {
	return itr.valid
}

func (itr *MDBIterator) Error() error {
	return itr.err
}

func (itr *MDBIterator) getCurrent() {
	itr.key, itr.value, itr.err = itr.c.Get(nil, mdb.GET_CURRENT)
	itr.setState()
}

func (itr *MDBIterator) Seek(key []byte) {
	itr.key, itr.value, itr.err = itr.c.Get(key, mdb.SET_RANGE)
	itr.setState()
}
func (itr *MDBIterator) Next() {
	itr.key, itr.value, itr.err = itr.c.Get(nil, mdb.NEXT)
	itr.setState()
}

func (itr *MDBIterator) Prev() {
	itr.key, itr.value, itr.err = itr.c.Get(nil, mdb.PREV)
	itr.setState()
}

func (itr *MDBIterator) First() {
	itr.key, itr.value, itr.err = itr.c.Get(nil, mdb.FIRST)
	itr.setState()
}

func (itr *MDBIterator) Last() {
	itr.key, itr.value, itr.err = itr.c.Get(nil, mdb.LAST)
	itr.setState()
}

func (itr *MDBIterator) setState() {
	if itr.err != nil {
		if itr.err == mdb.NotFound {
			itr.err = nil
		}
		itr.valid = false
	}
}

func (itr *MDBIterator) Close() error {
	if err := itr.c.Close(); err != nil {
		itr.tx.Abort()
		return err
	}
	if itr.err != nil {
		itr.tx.Abort()
		return itr.err
	}
	return itr.tx.Commit()
}

func (_ MDB) Name() string {
	return "lmdb"
}

func (db MDB) Path() string {
	return db.path
}

func (db MDB) Compact() {
}

func (db MDB) iterator(rdonly bool) *MDBIterator {
	flags := uint(0)
	if rdonly {
		flags = mdb.RDONLY
	}
	tx, err := db.env.BeginTxn(nil, flags)
	if err != nil {
		return &MDBIterator{nil, nil, nil, nil, false, err}
	}

	c, err := tx.CursorOpen(db.db)
	if err != nil {
		tx.Abort()
		return &MDBIterator{nil, nil, nil, nil, false, err}
	}

	return &MDBIterator{nil, nil, c, tx, true, nil}
}

func (db MDB) Close() error {
	db.env.DBIClose(db.db)
	if err := db.env.Close(); err != nil {
		panic(err)
	}
	return nil
}

func (db MDB) NewIterator() driver.IIterator {
	return db.iterator(true)
}

func (db MDB) NewWriteBatch() driver.IWriteBatch {
	return &WriteBatch{&db, []Write{}}
}
