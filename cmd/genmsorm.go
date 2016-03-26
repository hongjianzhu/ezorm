// Copyright © 2016 NAME HERE <EMAIL ADDRESS>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	"github.com/ezbuy/ezorm/db"
	"github.com/ezbuy/ezorm/parser"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

// genmsormCmd represents the genmsorm command
var genmsormCmd = &cobra.Command{
	Use:   "genmsorm",
	Short: "Generate sql server orm code",
	Run: func(cmd *cobra.Command, args []string) {
		db.SetDBConfig(&db.SqlDbConfig{
			SqlConnStr: dbConfig,
		})
		if table != "all" {
			handler(table)
		} else {
			tables := getAllTables()
			for _, t := range tables {
				handler(t)
			}
		}

		fmt.Println("genmsorm called")
	},
}

var table string
var outputYaml string
var dbConfig string

type ColumnInfo struct {
	ColumnName    string        `db:"ColumnName"`
	DataType      string        `db:"DataType"`
	MaxLength     int           `db:"MaxLength"`
	Nullable      bool          `db:"Nullable"`
	IsPrimaryKey  bool          `db:"IsPrimaryKey"`
	Sort          int           `db:"Sort"`
	IndexId       sql.NullInt64 `db:"IndexId"`
	IndexColumnId sql.NullInt64 `db:"IndexColumnId"`
	IsUnique      sql.NullBool  `db:"IsUnique"`
}

func handler(table string) {
	columnsinfo := getColumnInfo(table)
	createYamlFile(table, columnsinfo)
	generate(table)
}

func getAllTables() (tables []string) {
	query := `SELECT name FROM sys.tables`
	err := db.Query(&tables, query)
	if err != nil {
		panic(err)
	}

	return tables
}

func createYamlFile(table string, columns []*ColumnInfo) {
	objs := mapper(table, columns)
	bs, err := yaml.Marshal(objs)
	if err != nil {
		panic(err)
	}
	fileName := outputYaml + "/" + strings.ToLower(table) + "_mssql.yaml"
	ioutil.WriteFile(fileName, bs, 0644)
}

func getIndexInfo(columns []*ColumnInfo) (multiColumnIndexes, multiColumnUniques [][]string,
	singleColumnIndexSet, singleColumnUniqueSet map[int64]struct{}) {
	indexIdToColumns := make(map[int64][]*ColumnInfo)
	for _, v := range columns {
		indexId := v.IndexId.Int64
		if indexId > 0 && !v.IsPrimaryKey {
			indexIdToColumns[indexId] = append(indexIdToColumns[indexId], v)
		}
	}

	singleColumnIndexSet = make(map[int64]struct{})
	singleColumnUniqueSet = make(map[int64]struct{})
	for indexId, indexColums := range indexIdToColumns {
		if len(indexColums) == 1 {
			if indexColums[0].IsUnique.Bool {
				singleColumnUniqueSet[indexId] = struct{}{}
			} else {
				singleColumnIndexSet[indexId] = struct{}{}
			}
		} else {
			columnNames := make([]string, 0, len(indexColums))
			// Note: columns are sorted by IndexColumdId
			for _, c := range indexColums {
				columnNames = append(columnNames, c.ColumnName)
			}

			if indexColums[0].IsUnique.Bool {
				multiColumnUniques = append(multiColumnUniques, columnNames)
			} else {
				multiColumnIndexes = append(multiColumnIndexes, columnNames)
			}
		}
	}

	return
}

type tbl struct {
	DB      string        `yaml:"db"`
	Fields  []interface{} `yaml:"fields"`
	Indexes [][]string    `yaml:"indexes,flow"`
	Uniques [][]string    `yaml:"uniques,flow"`
}

func mapper(table string, columns []*ColumnInfo) map[string]*tbl {
	multiColumnIndexes, multiColumnUniques, singleColumnIndexSet, singleColumnUniqueSet := getIndexInfo(columns)

	var t tbl
	t.DB = "mssql"
	t.Indexes = multiColumnIndexes
	t.Uniques = multiColumnUniques
	objs := make(map[string]*tbl)
	objs[table] = &t
	fields := make([]interface{}, len(columns))
	for i, v := range columns {
		dataitem := make(map[string]interface{}, len(columns))
		dataitem[v.ColumnName] = parser.DbToGoType(v.DataType)
		if dataitem[v.ColumnName] == "time.Time" {
			parser.HaveTime = true
		}

		if _, ok := singleColumnUniqueSet[v.IndexId.Int64]; ok {
			dataitem["flags"] = []string{"unique"}
		} else if _, ok := singleColumnIndexSet[v.IndexId.Int64]; ok {
			dataitem["flags"] = []string{"index"}
		}

		fields[i] = dataitem
	}
	t.Fields = fields
	return objs
}

func getColumnInfo(table string) []*ColumnInfo {
	// Note: sort columns by IndexId and IndexColumnId to simplify later process
	query := `SELECT DISTINCT c.name AS ColumnName, t.Name AS DataType, c.max_length AS MaxLength,
    c.is_nullable AS Nullable, ISNULL(i.is_primary_key, 0) AS IsPrimaryKey ,c.column_id AS Sort,
	i.index_id AS IndexId, ic.index_column_id AS IndexColumnId, i.is_unique AS IsUnique
	FROM
    sys.columns c
	INNER JOIN
    sys.types t ON c.user_type_id = t.user_type_id
	LEFT OUTER JOIN
    sys.index_columns ic ON ic.object_id = c.object_id AND ic.column_id = c.column_id
	LEFT OUTER JOIN
    sys.indexes i ON ic.object_id = i.object_id AND ic.index_id = i.index_id
	WHERE
    c.object_id = OBJECT_ID(?) ORDER BY IndexId, IndexColumnId`

	server := db.GetSqlServer()
	var columninfos []*ColumnInfo
	err := server.Query(&columninfos, query, table)
	if err != nil {
		panic(err)
	}

	return columninfos
}

func generate(table string) {
	var objs map[string]map[string]interface{}
	fileName := outputYaml + "/" + strings.ToLower(table) + "_mssql.yaml"
	data, _ := ioutil.ReadFile(fileName)
	_, err := os.Stat(fileName)
	if err != nil {
		panic(err)
	}
	err = yaml.Unmarshal([]byte(data), &objs)

	for key, obj := range objs {
		metaObj := new(parser.Obj)
		metaObj.Package = strings.ToLower(table)
		metaObj.Name = key
		metaObj.Db = obj["db"].(string)
		err := metaObj.Read(obj)
		if err != nil {
			panic(err)
		}

		for _, genType := range metaObj.GetGenTypes() {
			file, err := os.OpenFile(output+"/gen_"+metaObj.Name+"_"+genType+".go", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			metaObj.TplWriter = file
			if err != nil {
				panic(err)
			}

			err = parser.Tpl.ExecuteTemplate(file, genType, metaObj)
			file.Close()
			if err != nil {
				panic(err)
			}
		}
	}

	cmd := exec.Command("gofmt", "-w", output)
	cmd.Run()
}

func init() {
	RootCmd.AddCommand(genmsormCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// genmsormCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// genmsormCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
	genmsormCmd.PersistentFlags().StringVarP(&table, "table", "t", "all", "table name, 'all' meaning all tables")
	genmsormCmd.PersistentFlags().StringVarP(&output, "output", "o", "", "output path")
	genmsormCmd.PersistentFlags().StringVarP(&outputYaml, "output yaml", "y", "", "output *.yaml path")
	genmsormCmd.PersistentFlags().StringVarP(&dbConfig, "db config", "d", "", "database configuration")
}
