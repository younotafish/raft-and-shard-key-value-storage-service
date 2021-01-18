package main

import "fmt"

func main() {
	ich:=make(chan int)
	ich <- 1
	<-ich
	//select {
	//case <-ich:
	//	println("pass")
	//}
	fmt.Println("no pass")

}