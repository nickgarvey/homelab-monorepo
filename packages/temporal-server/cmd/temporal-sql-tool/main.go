// temporal-sql-tool from the same go.temporal.io/server version as the server,
// with its schemas embedded (--schema-name postgresql/v12/temporal), so the
// Deployment's schema init containers run the same image as the server.
package main

import (
	"os"

	_ "go.temporal.io/server/common/persistence/sql/sqlplugin/postgresql" // the shared postgres
	"go.temporal.io/server/tools/sql"
)

func main() {
	if err := sql.RunTool(os.Args); err != nil {
		os.Exit(1)
	}
}
