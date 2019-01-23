#!/bin/bash

case "$1" in
    menderDownload)
        # The sleep is just to make the code path predictable, so that coverage
        # doesn't fluctuate. The client loop should have spun already by the
        # time we get here.
        sleep 1
        exit 0
        ;;
    moduleDownload)
        count=0
        while name=$(cat stream-next); do
            if [ -z "$name" ]; then
                break
            fi
            cat $name > tmp/module-downloaded-file$count
            count=$(($count+1))
        done
        exit 0
        ;;
    moduleDownloadFailure)
        cat stream-next > /dev/null
        exit 1
        ;;
    moduleDownloadStreamNextShortRead)
        dd if=stream-next of=/dev/null bs=1 count=1
        exit 0
        ;;
    moduleDownloadStreamShortRead)
        name=$(cat stream-next)
        dd if=$name of=/dev/null bs=1 count=1
        exit 0
        ;;
esac

exit 1
