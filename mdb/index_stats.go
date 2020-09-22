// Copyright 2018 Kuei-chun Chen. All rights reserved.

package mdb

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/simagix/gox"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// IndexStats holder indexes reader struct
type IndexStats struct {
	Databases []Database `bson:"databases"`
	Logger    *Logger    `bson:"keyhole"`

	filename string
	nocolor  bool
	verbose  bool
	version  string
}

// Accesses stores index accesses
type Accesses struct {
	Ops   int       `json:"ops" bson:"ops"`
	Since time.Time `json:"since" bson:"since"`
}

// IndexUsage stores index accesses
type IndexUsage struct {
	Accesses Accesses `json:"accesses"`
	Host     string   `json:"host"`
	Name     string
	Shard    string `json:"shard"`
}

// MissingIndex Details
type MissingIndex struct {
	ID   ID  `json:"_id"`
	Size int `json:"size"`
}

// ID struct
type ID struct {
	C int `json:"c"`
}

// Index stores indexes stats
type Index struct {
	Background              bool   `json:"background" bson:"background,truncate"`
	Collation               bson.D `json:"collation" bson:"collation,truncate"`
	ExpireAfterSeconds      int32  `json:"expireAfterSeconds" bson:"expireAfterSeconds,truncate"`
	Key                     bson.D `json:"key" bson:"key,truncate"`
	Name                    string `json:"name" bson:"name,truncate"`
	PartialFilterExpression bson.D `json:"partialFilterExpression" bson:"partialFilterExpression,truncate"`
	Sparse                  bool   `json:"sparse" bson:"sparse,truncate"`
	Unique                  bool   `json:"unique" bson:"unique,truncate"`
	Version                 int32  `json:"v" bson:"v,truncate"`

	EffectiveKey string       `json:"effectiveKey" bson:"effectiveKey"`
	Fields       []string     `json:"fields" bson:"fields"`
	IsDupped     bool         `json:"isDupped" bson:"isDupped"`
	IsShardKey   bool         `json:"isShardkey" bson:"isShardkey"`
	KeyString    string       `json:"keyString" bson:"keyString"`
	TotalOps     int          `json:"totalOps" bson:"totalOps"`
	Usage        []IndexUsage `json:"usage" bson:"usage"`
}

// NewIndexStats establish seeding parameters
func NewIndexStats(version string) *IndexStats {
	hostname, _ := os.Hostname()
	return &IndexStats{version: version, Logger: NewLogger(version, "-index"),
		filename: hostname + "-index.bson.gz", Databases: []Database{}}
}

// SetFilename sets output file name
func (ix *IndexStats) SetFilename(filename string) {
	ix.filename = strings.Replace(filename, ":", "_", -1)
}

// SetClusterDetailsFromFile File sets cluster details from a file
func (ix *IndexStats) SetClusterDetailsFromFile(filename string) error {
	if strings.HasSuffix(filename, "-index.bson.gz") == false &&
		strings.HasSuffix(filename, "-stats.bson.gz") == false {
		return errors.New("unsupported file type")
	}
	var data []byte
	var err error
	var fd *bufio.Reader
	if fd, err = gox.NewFileReader(filename); err != nil {
		return err
	}
	if data, err = ioutil.ReadAll(fd); err != nil {
		return err
	}
	return bson.Unmarshal(data, &ix)
}

// SetNoColor set nocolor flag
func (ix *IndexStats) SetNoColor(nocolor bool) {
	ix.nocolor = nocolor
}

// SetVerbose sets verbose level
func (ix *IndexStats) SetVerbose(verbose bool) {
	ix.verbose = verbose
}

// GetIndexes list all indexes of collections of databases
func (ix *IndexStats) GetIndexes(client *mongo.Client) ([]Database, error) {
	var err error
	var dbNames []string
	var collections []Collection
	ix.Databases = []Database{}
	var databases []Database
	if dbNames, err = ListDatabaseNames(client); err != nil {
		return databases, err
	}
	cnt := 0
	for _, name := range dbNames {
		if name == "admin" || name == "config" || name == "local" {
			if ix.verbose == true {
				log.Println("Skip", name)
			}
			continue
		}
		cnt++
		if ix.verbose == true {
			log.Println("checking", name)
		}
		if collections, err = ix.GetIndexesFromDB(client, name); err != nil {
			return ix.Databases, err
		}
		ix.Databases = append(ix.Databases, Database{Name: name, Collections: collections})
	}
	if cnt == 0 && ix.verbose == true {
		log.Println("No database is available")
	}
	ix.Logger.Add(fmt.Sprintf(`GetIndexes ends`))
	return ix.Databases, err
}

// GetIndexesFromDB list all indexes of collections of a database
func (ix *IndexStats) GetIndexesFromDB(client *mongo.Client, db string) ([]Collection, error) {
	var err error
	var cur *mongo.Cursor
	var ctx = context.Background()
	var collections []Collection
	if ix.verbose {
		fmt.Println("GetIndexesFromDB()", db)
	}
	if cur, err = client.Database(db).ListCollections(ctx, bson.M{}); err != nil {
		return collections, err
	}
	defer cur.Close(ctx)
	collectionNames := []string{}
	for cur.Next(ctx) {
		var elem struct {
			Name string `bson:"name"`
			Type string `bson:"type"`
		}
		if err = cur.Decode(&elem); err != nil {
			continue
		}
		if strings.HasPrefix(elem.Name, "system.") || elem.Type != "collection" {
			if ix.verbose == true {
				log.Println("skip", elem.Name)
			}
			continue
		}
		collectionNames = append(collectionNames, elem.Name)
	}

	sort.Strings(collectionNames)
	for _, v := range collectionNames {
		var collection = Collection{NS: db + "." + v, Name: v}
		if collection.Indexes, err = ix.GetIndexesFromCollection(client, client.Database(db).Collection(v)); err != nil {
			return collections, err
		}
		collections = append(collections, collection)
	}
	return collections, nil
}

// GetIndexesFromCollection gets indexes from a collection
func (ix *IndexStats) GetIndexesFromCollection(client *mongo.Client, collection *mongo.Collection) ([]Index, error) {
	var err error
	var ctx = context.Background()
	var pipeline = MongoPipeline(`{"$indexStats": {}}`)
	var list []Index
	var icur *mongo.Cursor
	var scur *mongo.Cursor
	var shardCount int64
	// get shard Count
	shardCount = GetShardsCount(client)
	db := collection.Database().Name()
	ix.Logger.Add(fmt.Sprintf(`GetIndexesFromCollection from %v.%v`, db, collection.Name()))

	var indexStats = []IndexUsage{}
	if scur, err = collection.Aggregate(ctx, pipeline); err != nil {
		log.Println(err)
	} else {
		for scur.Next(ctx) {
			var result IndexUsage
			if err = scur.Decode(&result); err != nil {
				log.Println(err)
				continue
			}
			indexStats = append(indexStats, result)
		}
		scur.Close(ctx)
	}

	cmd := bson.D{{Key: "listIndexes", Value: collection.Name()}}
	if icur, err = client.Database(db).RunCommandCursor(ctx, cmd); err != nil {
		log.Println(err)
		return list, err
	}
	defer icur.Close(ctx)

	for icur.Next(ctx) {
		o := Index{}
		if err = icur.Decode(&o); err != nil {
			log.Println(err)
			continue
		}

		var strbuf bytes.Buffer
		fields := []string{}
		for n, value := range o.Key {
			fields = append(fields, value.Key)
			if n == 0 {
				strbuf.WriteString("{ ")
			}
			strbuf.WriteString(value.Key + ": " + fmt.Sprint(value.Value))
			if n == len(o.Key)-1 {
				strbuf.WriteString(" }")
			} else {
				strbuf.WriteString(", ")
			}
		}
		o.Fields = fields
		o.KeyString = strbuf.String()
		// Check shard keys
		var v map[string]interface{}
		ns := collection.Database().Name() + "." + collection.Name()
		if ix.verbose {
			log.Println("GetIndexesFromCollection", ns, o.KeyString)
		}
		if err = client.Database("config").Collection("collections").FindOne(ctx, bson.M{"_id": ns, "key": o.Key}).Decode(&v); err == nil {
			o.IsShardKey = true
		}

		o.EffectiveKey = strings.Replace(o.KeyString[2:len(o.KeyString)-2], ": -1", ": 1", -1)
		o.Usage = []IndexUsage{}
		for _, result := range indexStats {
			if result.Name == o.Name {
				o.TotalOps += result.Accesses.Ops
				o.Usage = append(o.Usage, result)
			}
		}
		list = append(list, o)
	}
	sort.Slice(list, func(i, j int) bool { return (list[i].EffectiveKey < list[j].EffectiveKey) })
	for i, o := range list {
		if o.KeyString != "{ _id: 1 }" && o.IsShardKey == false {
			list[i].IsDupped = checkIfDupped(o, list)
			if int64(len(list[i].Usage)) < shardCount {
				log.Println(o.EffectiveKey)
			}
		}
	}
	return list, nil
}

// check if an index is a dup of others
func checkIfDupped(doc Index, list []Index) bool {
	for _, o := range list {
		// check indexes if not marked as dupped, has the same first field, and more or equal number of fields
		if o.IsDupped == false && doc.Fields[0] == o.Fields[0] && doc.KeyString != o.KeyString && len(o.Fields) >= len(doc.Fields) {
			nmatched := 0
			for i, fld := range doc.Fields {
				if i == 0 {
					continue
				}
				for j, field := range o.Fields {
					if j > 0 && fld == field {
						nmatched++
						break
					}
				}
			}
			if nmatched == len(doc.Fields)-1 {
				return true
			}
		}
	}
	return false
}

// OutputBSON writes index stats bson to a file
func (ix *IndexStats) OutputBSON() error {
	var err error
	var bsond bson.D
	var buf []byte
	if buf, err = bson.Marshal(ix); err != nil {
		return err
	}
	bson.Unmarshal(buf, &bsond)
	if buf, err = bson.Marshal(bsond); err != nil {
		return err
	}
	outdir := "./out/"
	os.Mkdir(outdir, 0755)
	ofile := outdir + ix.filename
	if err = gox.OutputGzipped(buf, ofile); err == nil {
		fmt.Println("Index stats is written to", ofile)
	}
	return err
}

// OutputJSON writes json data to a file
func (ix *IndexStats) OutputJSON() error {
	var err error
	var data []byte
	if data, err = bson.MarshalExtJSON(ix, false, false); err != nil {
		return err
	}
	outdir := "./out/"
	os.Mkdir(outdir, 0755)
	ofile := outdir + strings.ReplaceAll(filepath.Base(ix.filename), "bson.gz", "json")
	ioutil.WriteFile(ofile, data, 0644)
	fmt.Println("json data written to", ofile)
	return err
}

// Print prints indexes
func (ix *IndexStats) Print() {
	ix.PrintIndexesOf(ix.Databases)
	if ix.verbose {
	}
}

// PrintIndexesOf prints indexes
func (ix *IndexStats) PrintIndexesOf(databases []Database) {
	for _, db := range databases {
		for _, coll := range db.Collections {
			var buffer bytes.Buffer
			ns := coll.NS
			buffer.WriteString("\n")
			buffer.WriteString(ns)
			buffer.WriteString(":\n")
			for _, o := range coll.Indexes {
				font := codeDefault
				tailCode := codeDefault
				if ix.nocolor {
					font = ""
					tailCode = ""
				}
				if o.KeyString == "{ _id: 1 }" {
					buffer.WriteString(fmt.Sprintf("%v  %v%v", font, o.KeyString, tailCode))
				} else if o.IsShardKey == true {
					buffer.WriteString(fmt.Sprintf("%v* %v%v", font, o.KeyString, tailCode))
				} else if o.IsDupped == true {
					if ix.nocolor == false {
						font = codeRed
					}
					buffer.WriteString(fmt.Sprintf("%vx %v%v", font, o.KeyString, tailCode))
				} else if o.TotalOps == 0 {
					if ix.nocolor == false {
						font = codeBlue
					}
					buffer.WriteString(fmt.Sprintf("%v? %v%v", font, o.KeyString, tailCode))
				} else {
					buffer.WriteString(fmt.Sprintf("  %v", o.KeyString))
				}

				for _, u := range o.Usage {
					buffer.Write([]byte("\n\thost: " + u.Host + ", ops: " + fmt.Sprintf("%v", u.Accesses.Ops) + ", since: " + fmt.Sprintf("%v", u.Accesses.Since)))
				}
				buffer.WriteString("\n")
			}
			fmt.Println(buffer.String())
		}
	}
}

// CreateIndexes creates indexes
func (ix *IndexStats) CreateIndexes(client *mongo.Client) error {
	var ctx = context.Background()
	var err error
	for _, db := range ix.Databases {
		for _, coll := range db.Collections {
			collection := client.Database(db.Name).Collection(coll.Name)
			for _, o := range coll.Indexes {
				if o.IsShardKey == true {
					// TODO
				}
				var indexKey bson.D
				for _, field := range o.Fields {
					for _, e := range o.Key {
						if field == e.Key {
							indexKey = append(indexKey, e)
							break
						}
					}
				}

				opt := options.Index()
				opt.SetVersion(o.Version)
				opt.SetName(o.Name)
				if o.Background == true {
					opt.SetBackground(o.Background)
				}
				if o.ExpireAfterSeconds > 0 {
					opt.SetExpireAfterSeconds(o.ExpireAfterSeconds)
				}
				if o.Unique == true {
					opt.SetUnique(o.Unique)
				}
				if o.Sparse == true {
					opt.SetSparse(o.Sparse)
				}
				if o.Collation != nil {
					var collation *options.Collation
					if data, err := bson.Marshal(o.Collation); err != nil {
						fmt.Println(err)
					} else {
						bson.Unmarshal(data, &collation)
						opt.SetCollation(collation)
					}
				}
				if o.PartialFilterExpression != nil {
					opt.SetPartialFilterExpression(o.PartialFilterExpression)
				}
				if _, err = collection.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: o.Key, Options: opt}); err != nil {
					fmt.Println(err)
				}
			}
		}
	}
	return err
}

// ListDatabaseNames gets all database names
func ListDatabaseNames(client *mongo.Client) ([]string, error) {
	var err error
	var names []string
	var result mongo.ListDatabasesResult
	if result, err = client.ListDatabases(context.Background(), bson.M{}); err != nil {
		return names, err
	}
	for _, db := range result.Databases {
		names = append(names, db.Name)
	}
	return names, err
}

// GetShardsCount return count of all the shards
func GetShardsCount(client *mongo.Client) (count int64) {
	ctx := context.Background()

	shardCount, err := client.Database("config").Collection("shards").CountDocuments(ctx, bson.D{})
	_ = err
	return shardCount
}
