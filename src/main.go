package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"./mixer"
)

const PORT = "8080"
const ROUTE = "/mixer"

func main() {
	http.Handle(ROUTE, &mixer.MixerAPI{})
	println("listen on ", PORT)
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	println("try to visit:")
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok {
			if ipnet.IP.To4() != nil {
				println("http://" + ipnet.IP.String() + ":" + string(PORT) + ROUTE)
			}
		}
	}
	log.Fatal(http.ListenAndServe(":8080", nil))
}
