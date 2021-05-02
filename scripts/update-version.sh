#!/usr/bin/env bash

export PROJECTDIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && cd .. && pwd )"

vfile=$1; shift
if [[ -z $vfile ]]; then
    vfile="$PROJECTDIR/version.txt"
fi

values=($(cat $vfile))

cat << EOF > $vfile
${values[0]}
$(date +%Y%m%d-%H%M)
$((${values[2]} + 1))
EOF
