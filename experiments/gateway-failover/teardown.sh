#!/bin/bash
for c in fo-eu fo-us; do kind delete cluster --name "$c"; done
