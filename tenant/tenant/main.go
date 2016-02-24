// Copyright (c) 2016 Pani Networks
// All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

// Command for running the Tenant service.
package main

import (
	"flag"
	"fmt"

	"github.com/romana/core/tenant"
)

// Main entry point for the tenant microservice
func main() {
	createSchema := flag.Bool("createSchema", false, "Create schema")
	overwriteSchema := flag.Bool("overwriteSchema", false, "Overwrite schema")
	rootUrl := flag.String("rootUrl", "", "Root service URL")
	flag.Parse()

	if *createSchema || *overwriteSchema {
		err := tenant.CreateSchema(*rootUrl, *overwriteSchema)
		if err != nil {
			panic(err)
		}
		fmt.Println("Schema created.")
		return
	}

	svcInfo, err := tenant.Run(*rootUrl)
	if err != nil {
		panic(err)
	}
	for {
		msg := <-svcInfo.Channel
		fmt.Println(msg)
	}
}
