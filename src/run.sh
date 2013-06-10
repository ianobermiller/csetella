#! /bin/bash

until ./src; do
	echo "CSEtella crashed with exit code $?. Restarting..." >&2
	sleep 1
done
