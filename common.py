#!/usr/bin/python

from fabric.api import *
import fabric.network

import pytest
import os
import signal
import subprocess
import time

import conftest

# Return Popen object
def start_qemu():
    proc = subprocess.Popen("../poky/meta-mender-qemu/scripts/mender-qemu")
    execute(qemu_prep_after_boot, hosts = conftest.current_hosts())
    return proc


def kill_qemu():
    os.system("pkill qemu-system-arm")


def reboot(wait = 60):
    with settings(warn_only = True):
        sudo("reboot")

    # Make sure reboot has had time to take effect.
    time.sleep(5)

    fabric.network.disconnect_all()

    run_after_connect("true", wait = wait)

    qemu_prep_after_boot()


def run_after_connect(cmd, wait = 60):
    output = ""
    start_time = time.time()
    # Use shorter timeout to get a faster cycle.
    with settings(timeout = 5, abort_exception = Exception):
        while True:
            attempt_time = time.time()
            try:
                output = run(cmd)
                break
            except Exception as e:
                print("Could not connect to host %s: %s" % (env.host_string, e))
                if attempt_time >= start_time + wait:
                    raise Exception("Could not reconnect to QEMU")
                now = time.time()
                if now - attempt_time < 5:
                    time.sleep(5 - (now - attempt_time))
                continue
    return output


def ssh_prep_args():
    return ssh_prep_args_impl("ssh")


def scp_prep_args():
    return ssh_prep_args_impl("scp")


def ssh_prep_args_impl(tool):
    if not env.host_string:
        raise Exception("get()/put() called outside of execute()")

    cmd = ("%s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" %
           tool)

    host_parts = env.host_string.split(":")
    host = ""
    port = ""
    port_flag = "-p"
    if tool == "scp":
        port_flag = "-P"
    if len(host_parts) == 2:
        host = host_parts[0]
        port = "%s%s" % (port_flag, host_parts[1])
    elif len(host_parts) == 1:
        host = host_parts[0]
        port = ""
    else:
        raise Exception("Malformed host string")

    return (cmd, host, port)


# Yocto build SSH is lacking SFTP, let's override and use regular SCP instead.
def put(file, local_path = ".", remote_path = "."):
    (scp, host, port) = scp_prep_args()

    local("%s %s %s/%s %s@%s:%s" %
          (scp, port, local_path, file, env.user, host, remote_path))


# See comment for put().
def get(file, local_path = ".", remote_path = "."):
    (scp, host, port) = scp_prep_args()

    local("%s %s %s@%s:%s/%s %s" %
          (scp, port, env.user, host, remote_path, file, local_path))


def qemu_prep_after_boot():
    # TODO: It is unknown why this is needed. "/u-boot" with "auto"
    # attribute is in fstab already... This should be removed.
    run_after_connect("mount /u-boot || true")


def qemu_prep_fresh_host():
    # Nothing needed ATM.
    # Uncomment this if you want to debug mender (beware that if you reboot to
    # a new filesystem you may have to upload there as well.
    #put("mender")

    pass


@pytest.fixture(scope = "module")
def qemu_running():
    kill_qemu()
    start_qemu()
    execute(qemu_prep_fresh_host, hosts = conftest.current_hosts())


@pytest.fixture(scope = "function")
def no_image_file(qemu_running):
    execute(no_image_file_impl, hosts = conftest.current_hosts())


def no_image_file_impl():
    run("rm -f image.dat")
