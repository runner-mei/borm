package borm

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/boltdb/bolt"
)

type TSEngine struct {
	basePath    string
	nameWith    func(t time.Time) string
	currentFile string
	store       *Store
	bkt         *Bucket
}

func (db *TSEngine) Close() error {
	var err error
	if db.store != nil {
		err = db.store.Close()

		db.store = nil
		db.bkt = nil
	}
	return err
}

func (db *TSEngine) EnforceRetention(t time.Time) error {
	shards, err := ListShards(db.basePath, t.Location())
	if err != nil {
		return err
	}
	return db.removeShardsBefore(shards, t)
}

func (db *TSEngine) removeShardsBefore(shards Shards, t time.Time) error {
	for _, shard := range shards {
		if shard.startTime.Before(t) {
			if strings.ToLower(filepath.Base(shard.path)) ==
				strings.ToLower(db.currentFile) {
				if err := db.Close(); err != nil {
					return err
				}
			}
			if err := os.Remove(shard.path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (db *TSEngine) open(file string) (*Store, *Bucket, error) {
	store, err := Open(file, 0666, &bolt.Options{Timeout: 10 * time.Second})
	if err != nil {
		return nil, nil, err
	}
	bkt, err := store.CreateBucketIfNotExists("attack", nil, nil)
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	return store, bkt, nil
}

func (db *TSEngine) ensureOpen(t time.Time) error {
	newFile := db.nameWith(t)
	if db.currentFile != newFile {
		db.Close()
		db.currentFile = newFile
	}

	if db.store == nil {
		var err error
		db.store, db.bkt, err = db.open(db.currentFile)
		if err != nil {
			return err
		}
	}
	return nil
}

func (db *TSEngine) Write(t time.Time, cb func(bkt *Bucket) error) error {
	err := db.ensureOpen(t)
	if err != nil {
		return err
	}
	return cb(db.bkt)
}

func (db *TSEngine) Read(start, end time.Time, cb func(bkt *Bucket) error) error {
	return filesRead(db.nameWith, start, end, func(position int, fileName string) error {
		return db.read(fileName, cb)
	})
}

func (db *TSEngine) Get(id string, record interface{}) error {
	time := TimeFromID(id)
	if time.IsZero() {
		return ErrKeyExists
	}

	fileName := db.nameWith(time)
	return db.read(fileName, func(bkt *Bucket) error {
		return bkt.Get(id, record)
	})
}

func (db *TSEngine) read(fileName string, cb func(bkt *Bucket) error) error {
	if fileName == db.currentFile {
		if db.store == nil {
			store, bkt, err := db.open(db.currentFile)
			if err != nil {
				return err
			}
			db.store = store
			db.bkt = bkt
		}

		return cb(db.bkt)
	}
	store, bkt, err := db.open(fileName)
	if err != nil {
		return err
	}
	defer store.Close()

	return cb(bkt)
}

func (db *TSEngine) Query(start, end time.Time, cb func(it *Iterator) error) error {
	startID := CreateID(start, 0)
	endID := CreateID(end, 0)

	return filesRead(db.nameWith, start, end, func(position int, fileName string) error {
		return db.read(fileName, func(bkt *Bucket) error {
			switch position {
			case positionStart:
				return bkt.GetRange(startID, "", cb)
			case positionEnd:
				return bkt.GetRange("", endID, cb)
			case positionStartEnd:
				return bkt.GetRange(startID, endID, cb)
			default:
				return bkt.GetRange("", "", cb)
			}
		})
	})
}

const positionMiddle = 0
const positionStart = 1
const positionEnd = 2
const positionStartEnd = 3

type fileCallback func(position int, fileName string) error

func filesRead(nameWith func(t time.Time) string, start, end time.Time, cb fileCallback) error {
	if start.After(end) {
		return errors.New("time range is invalid")
	}

	startY := start.Year()
	startYD := start.YearDay()
	endY := end.Year()
	endYD := end.YearDay()

	current := start

	currentY := current.Year()
	currentYD := current.YearDay()

	for currentY < endY || (currentY == endY && currentYD <= endYD) {
		///handle := cb
		//inStart := currentY == startY && currentYD == startYD
		//inEnd := currentY == endY && currentYD == endYD

		fileName := nameWith(current)
		position := positionMiddle
		if currentY == startY && currentYD == startYD {
			if currentY == endY && currentYD == endYD {
				position = positionStartEnd
			} else {
				position = positionStart
			}
		} else if currentY == endY && currentYD == endYD {
			position = positionEnd
		}

		if err := cb(position, fileName); nil != err {
			return err
		}

		current = current.Add(24 * time.Hour)
		currentY = current.Year()
		currentYD = current.YearDay()
	}
	return nil
}

func OpenTSEngine(path string, nameWith func(t time.Time) string) (*TSEngine, error) {
	return &TSEngine{
		basePath: path,
		nameWith: func(t time.Time) string {
			return filepath.Join(path, nameWith(t))
		}}, nil
}

func OpenTS(path string) (*TSEngine, error) {
	return &TSEngine{
		basePath: path,
		nameWith: func(t time.Time) string {
			return filepath.Join(path, strconv.Itoa(t.Year())+"_"+strconv.Itoa(t.YearDay())+".ts")
		}}, nil
}
