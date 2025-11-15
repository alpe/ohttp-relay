package main

import (
	"fmt"
	"reflect"

	"github.com/confidentsecurity/ohttp"
)

func main() {
	t := reflect.TypeOf(ohttp.KeyConfig{})
	for i := 0; i < t.NumMethod(); i++ {
		m := t.Method(i)
		fmt.Println(m.Name)
	}
}
