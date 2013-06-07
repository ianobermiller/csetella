#!/bin/bash
go build

rm log*
./src -host=127.0.0.1 -log=log1 -port=20001 -secret=text1 -seed=127.0.0.1:5000&
./src -host=127.0.0.1 -log=log2 -port=20002 -secret=text2 -seed=127.0.0.1:20001&
./src -host=127.0.0.1 -log=log3 -port=20003 -secret=text3 -seed=127.0.0.1:20002&
./src -host=127.0.0.1 -log=log4 -port=20004 -secret=text4 -seed=127.0.0.1:20003&

read -p "Press enter to stop.
"
pkill src