#!/bin/bash

case "$1" in
    menderDownload)
        exit 0
        ;;
    moduleDownload)
        name=$(cat stream-next)
        cat $name > tmp/module-downloaded-file
        exit 0
        ;;
    moduleDownloadFailure)
        cat stream-next > /dev/null
        exit 1
        ;;
esac

exit 1
