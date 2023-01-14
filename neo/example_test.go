package neo_test

import (
	"fmt"

	"github.com/podhmo/reflect-shape/neo"
)

// User is the object for User.
type User struct {
	// name of User.
	Name string
	Age  int // age of User.
}

func ExampleConfig() {
	cfg := neo.Config{IncludeGoTestFiles: true}
	shape := cfg.Extract(User{})

	fmt.Println(shape.Name, shape.Kind, shape.Package.Path)
	for _, f := range shape.MustStruct().Fields() {
		fmt.Println("--", f.Name, f.Doc)
	}

	// Output:
	// User struct github.com/podhmo/reflect-shape/neo_test
	// -- Name name of User.
	// -- Age age of User.
}
