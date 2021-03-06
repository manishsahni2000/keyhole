// Copyright 2018 Kuei-chun Chen. All rights reserved.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/simagix/gox"
	"github.com/simagix/keyhole/mdb"
	"github.com/simagix/keyhole/sim"
	"github.com/simagix/keyhole/sim/util"
	"github.com/simagix/mongo-atlas/atlas"
	anly "github.com/simagix/mongo-ftdc/analytics"
	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"
)

var version = "self-built"

func main() {
	allinfo := flag.Bool("allinfo", false, "get all cluster info")
	changeStreams := flag.Bool("changeStreams", false, "change streams watch")
	collection := flag.String("collection", "", "collection name to print schema")
	collscan := flag.Bool("collscan", false, "list only COLLSCAN (with --loginfo)")
	cardinality := flag.String("cardinality", "", "check collection cardinality")
	conn := flag.Int("conn", 0, "nuumber of connections")
	createIndex := flag.String("createIndex", "", "create indexes")
	diag := flag.String("diag", "", "diagnosis of server status or diagnostic.data")
	duration := flag.Int("duration", 5, "load test duration in minutes")
	drop := flag.Bool("drop", false, "drop examples collection before seeding")
	explain := flag.String("explain", "", "explain a query from a JSON doc or a log line")
	file := flag.String("file", "", "template file for seedibg data")
	ftdc := flag.Bool("ftdc", false, "download from atlas://user:key@group/cluster")
	index := flag.Bool("index", false, "get indexes info")
	info := flag.Bool("info", false, "get cluster info | Atlas info (atlas://user:key)")
	loginfo := flag.Bool("loginfo", false, "log performance analytic from file or Atlas")
	nocolor := flag.Bool("nocolor", false, "disable color codes")
	peek := flag.Bool("peek", false, "only collect stats")
	pause := flag.Bool("pause", false, "pause an Atlas cluster atlas://user:key@group/cluster")
	pipe := flag.String("pipeline", "", "aggregation pipeline")
	port := flag.Int("port", 5408, "web server port number")
	print := flag.String("print", "", "print contents of input file")
	redaction := flag.Bool("redact", false, "redact document")
	regex := flag.String("regex", "", "regex pattern for loginfo")
	request := flag.String("request", "", "Atlas API command")
	resume := flag.Bool("resume", false, "resume an Atlas cluster atlas://user:key@group/cluster")
	schema := flag.Bool("schema", false, "print schema")
	seed := flag.Bool("seed", false, "seed a database for demo")
	simonly := flag.Bool("simonly", false, "simulation only mode")
	sslCAFile := flag.String("sslCAFile", "", "CA file")
	sslPEMKeyFile := flag.String("sslPEMKeyFile", "", "client PEM file")
	tlsCAFile := flag.String("tlsCAFile", "", "TLS CA file")
	tlsCertificateKeyFile := flag.String("tlsCertificateKeyFile", "", "TLS CertificateKey File")
	tps := flag.Int("tps", 20, "number of trasaction per second per connection")
	total := flag.Int("total", 1000, "nuumber of documents to create")
	tx := flag.String("tx", "", "file with defined transactions")
	uri := flag.String("uri", "", "MongoDB URI") // orverides connection uri from args
	ver := flag.Bool("version", false, "print version number")
	verbose := flag.Bool("v", false, "verbose")
	vv := flag.Bool("vv", false, "very verbose")
	webserver := flag.Bool("web", false, "enable web server")
	wt := flag.Bool("wt", false, "visualize wiredTiger cache usage")
	yes := flag.Bool("yes", false, "bypass confirmation")

	flag.Parse()
	if *tlsCAFile == "" && *sslCAFile != "" {
		*tlsCAFile = *sslCAFile
	}
	if *tlsCertificateKeyFile == "" && *sslPEMKeyFile != "" {
		*tlsCertificateKeyFile = *sslPEMKeyFile
	}
	if *uri == "" && len(flag.Args()) > 0 {
		*uri = flag.Arg(0)
	}
	flagset := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { flagset[f.Name] = true })
	var err error
	if strings.HasPrefix(*uri, "atlas://") {
		var api *atlas.API
		if api, err = atlas.ParseURI(*uri); err != nil {
			log.Fatal(err)
		}
		api.SetArgs(flag.Args())
		api.SetFTDC(*ftdc)
		api.SetInfo(*info)
		api.SetLoginfo(*loginfo)
		api.SetPause(*pause)
		api.SetResume(*resume)
		api.SetRequest(*request)
		api.SetVerbose(*verbose)
		fmt.Println(api.Execute())

		if *loginfo {
			for _, filename := range api.GetLogNames() {
				fmt.Println("=> processing", filename)
				var str string
				li := mdb.NewLogInfo()
				li.SetKeyholeInfo(mdb.NewKeyholeInfo(version, "-loginfo"))
				li.SetVerbose(*verbose)
				if str, err = li.AnalyzeFile(filename, *redaction); err != nil {
					log.Println(err)
					continue
				}
				fmt.Println(str)
				if li.OutputFilename != "" {
					log.Println("Log info written to", li.OutputFilename)
					if *verbose { // encoded structure is deprecated, replaced with bson.gz
						filename := li.OutputFilename
						if idx := strings.LastIndex(filename, "-log.bson.gz"); idx > 0 {
							filename = filename[:idx] + "-log.enc"
						}
						log.Println("Encoded output written to", filename, "(deprecated)")
					}
				}
			}
		}
		os.Exit(0)
	} else if *webserver {
		filenames := append([]string{*diag}, flag.Args()...)
		addr := fmt.Sprintf(":%d", *port)
		if listener, err := net.Listen("tcp", addr); err != nil {
			log.Fatal(err)
		} else {
			listener.Close()
		}
		metrics := anly.NewMetrics()
		metrics.ProcessFiles(filenames)
		log.Fatal(http.ListenAndServe(addr, nil))
	} else if *diag != "" {
		filenames := append([]string{*diag}, flag.Args()...)
		metrics := anly.NewDiagnosticData()
		if str, e := metrics.PrintDiagnosticData(filenames); e != nil {
			log.Fatal(e)
		} else {
			fmt.Println(str)
		}
		os.Exit(0)
	} else if *loginfo {
		if len(flag.Args()) < 1 {
			log.Fatal("Usage: keyhole --loginfo filename")
		}
		filenames := []string{}
		for i, arg := range flag.Args() { // backward compatible
			if arg == "-collscan" || arg == "--collscan" {
				*collscan = true
			} else if arg == "-silent" || arg == "--silent" {
				*nocolor = true
			} else if arg == "-v" || arg == "--v" {
				*verbose = true
			} else if (arg == "-regex" || arg == "--regex") && *regex != "" {
				*regex = flag.Args()[i+1]
			} else {
				filenames = append(filenames, arg)
			}
		}
		li := mdb.NewLogInfo()
		li.SetKeyholeInfo(mdb.NewKeyholeInfo(version, "-loginfo"))
		li.SetRegexPattern(*regex)
		li.SetCollscan(*collscan)
		li.SetVerbose(*verbose)
		li.SetSilent(*nocolor)
		for _, filename := range filenames {
			var str string
			if str, err = li.AnalyzeFile(filename, *redaction); err != nil {
				log.Fatal(err)
			}
			fmt.Println(str)
			if li.OutputFilename != "" {
				log.Println("Log info written to", li.OutputFilename)
				if *verbose { // encoded structure is deprecated, replaced with bson.gz
					filename := li.OutputFilename
					if idx := strings.LastIndex(filename, "-log.bson.gz"); idx > 0 {
						filename = filename[:idx] + "-log.enc"
					}
					log.Println("Encoded output written to", filename, "(deprecated)")
				}
			}
		}
		os.Exit(0)
	} else if *ver {
		fmt.Println("keyhole", version)
		os.Exit(0)
	} else if *explain != "" && *uri == "" { //--explain file.json.gz (w/o uri)
		exp := mdb.NewExplain()
		if err = exp.PrintExplainResults(*explain); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	} else if *print != "" {
		if err := mdb.PrintBSON(*print); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	} else if len(*uri) == 0 {
		fmt.Println("Missing connection string")
		fmt.Println("Usage: keyhole [opts] uri")
		flag.PrintDefaults()
		os.Exit(0)
	}

	client, err := mdb.NewMongoClient(*uri, *tlsCAFile, *tlsCertificateKeyFile)
	if err != nil {
		log.Fatal(err)
	}
	connString, err := connstring.Parse(*uri)
	if err != nil {
		log.Fatal(err)
	}

	if *info == true || *allinfo == true {
		params := "-info"
		if *allinfo == true {
			*verbose = true
			*vv = true
			params = "-allinfo"
		} else if *vv == true {
			params = "-info -vv"
		} else if *verbose == true {
			params = "-info -v"
		}
		nConnections := 16
		if *conn != 0 {
			nConnections = *conn
		}
		mc := mdb.NewMongoCluster(client)
		mc.SetConnString(connString)
		mc.SetKeyholeInfo(mdb.NewKeyholeInfo(version, params))
		mc.SetNumberConnections(nConnections)
		mc.SetRedaction(*redaction)
		mc.SetVerbose(*verbose)
		mc.SetVeryVerbose(*vv)
		if doc, e := mc.GetClusterInfo(); e != nil {
			log.Fatal(e)
		} else if *verbose == false && *vv == false {
			fmt.Println(gox.Stringify(doc, "", "  "))
		}
		os.Exit(0)
	} else if *seed == true {
		f := sim.NewFeeder()
		f.SetCollection(*collection)
		f.SetDatabase(connString.Database)
		f.SetFile(*file)
		f.SetIsDrop(*drop)
		nConnection := 2 * runtime.NumCPU()
		if *conn != 0 {
			nConnection = *conn
		}
		f.SetNumberConnections(nConnection)
		f.SetTotal(*total)
		if err = f.SeedData(client); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	} else if *index == true {
		ix := mdb.NewIndexes(client)
		ix.SetNoColor(*nocolor)
		if connString.Database == mdb.KeyholeDB {
			connString.Database = ""
		}
		ix.SetDBName(connString.Database)
		ix.SetVerbose(*verbose)
		if indexesMap, ixe := ix.GetIndexes(); ixe != nil {
			log.Fatal(err)
		} else {
			ix.PrintIndexesOf(indexesMap)
			if err = ix.Save(); err != nil {
				log.Fatal(err)
			}
		}
		os.Exit(0)
	} else if *createIndex != "" {
		if *uri == "" {
			log.Fatal("Usage: keyhole --createIndex <filename>-index.bson.gz mongodb://<...>")
		}
		ix := mdb.NewIndexes(client)
		ix.SetNoColor(*nocolor)
		ix.SetVerbose(*verbose)
		if err = ix.SetIndexesMapFromFile(*createIndex); err != nil {
			log.Fatal(err)
		}
		if err = ix.CreateIndexes(); err != nil {
			log.Fatal(err)
		}
		if indexesMap, ixe := ix.GetIndexes(); ixe != nil {
			log.Fatal(err)
		} else {
			ix.PrintIndexesOf(indexesMap)
		}
		os.Exit(0)
	} else if *schema == true {
		if *collection == "" {
			log.Fatal("usage: keyhole [-v] --schema --collection collection_name <mongodb_uri>")
		}
		c := client.Database(connString.Database).Collection(*collection)
		var str string
		if str, err = sim.GetSchema(c, *verbose); err != nil {
			log.Fatal(err)
		}
		fmt.Println(str)
		os.Exit(0)
	} else if *cardinality != "" { // --card <collection> [-v]
		card := mdb.NewCardinality(client)
		card.SetVerbose(*verbose)
		if summary, e := card.GetCardinalityArray(connString.Database, *cardinality); e != nil {
			log.Fatal(e)
		} else {
			fmt.Println(card.GetSummary(summary))
		}
		os.Exit(0)
	} else if *explain != "" { // --explain json_or_log_file  [-v]
		exp := mdb.NewExplain()
		exp.SetVerbose(*verbose)
		if err = exp.ExecuteAllPlans(client, *explain); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	} else if *changeStreams == true {
		stream := mdb.NewChangeStream()
		stream.SetCollection(*collection)
		stream.SetDatabase(connString.Database)
		stream.SetPipelineString(*pipe)
		stream.Watch(client, util.Echo)
		os.Exit(0)
	}

	go func() {
		http.HandleFunc("/", gox.Cors(handler))
		addr := fmt.Sprintf(":%d", *port)
		log.Println(http.ListenAndServe(addr, nil))
	}()
	if *wt == true {
		wtc := mdb.NewWiredTigerCache(client)
		log.Printf("URL: http://localhost:%d/wt\n", *port)
		wtc.Start()
	}

	var runner *sim.Runner
	if runner, err = sim.NewRunner(*uri, *tlsCAFile, *tlsCertificateKeyFile); err != nil {
		log.Fatal(err)
	}
	runner.SetCollection(*collection)
	runner.SetTPS(*tps)
	runner.SetTemplateFilename(*file)
	runner.SetVerbose(*verbose)
	runner.SetSimulationDuration(*duration)
	runner.SetPeekingMode(*peek)
	runner.SetDropFirstMode(*drop)
	nConnection := runtime.NumCPU()
	if *conn != 0 {
		nConnection = *conn
	}
	runner.SetNumberConnections(nConnection)
	runner.SetTransactionTemplateFilename(*tx)
	runner.SetSimOnlyMode(*simonly)
	runner.SetAutoMode(*yes)
	if err = runner.Start(); err != nil {
		log.Fatal(err)
	}
	runner.CollectAllStatus()
}

func handler(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": 1, "message": "hello keyhole!"})
}
