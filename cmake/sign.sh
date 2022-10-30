#!/bin/bash
set -e
passage show build | minisign -S -s ${HOME}/.minisign/build.key -c "fb2converter for ${1} release signature" -m fb2c_${1}.zip
