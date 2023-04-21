// Copyright 2023 Northern.tech AS
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//        http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.

#include <common/io.hpp>

#include <sys/stat.h>
#include <fcntl.h>
#include <unistd.h>

namespace mender {
namespace common {
namespace io {

FileDescriptorReader::FileDescriptorReader(int fd) :
	fd_(fd) {
}

FileDescriptorReader::FileDescriptorReader() :
	fd_(-1) {
}

FileDescriptorReader::~FileDescriptorReader() {
	if (fd_ >= 0) {
		close(fd_);
	}
}

error::Error FileDescriptorReader::Open(const string &path) {
	if (fd_ >= 0) {
		close(fd_);
	}

	fd_ = open(path.c_str(), O_RDONLY);
	if (fd_ < 0) {
		int err = errno;
		return error::Error(generic_category().default_error_condition(err), "Cannot open " + path);
	}
	return error::NoError;
}

ExpectedSize FileDescriptorReader::Read(vector<uint8_t>::iterator start, vector<uint8_t>::iterator end) {
	auto n = read(fd_, &start[0], end - start);
	if (n < 0) {
		int err = errno;
		return expected::unexpected(error::Error(generic_category().default_error_condition(err), "read() failed"));
	}
	return n;
}

FileDescriptorWriter::FileDescriptorWriter(int fd) :
	fd_(fd) {
}

FileDescriptorWriter::FileDescriptorWriter() :
	fd_(-1) {
}

FileDescriptorWriter::~FileDescriptorWriter() {
	if (fd_ >= 0) {
		close(fd_);
	}
}

error::Error FileDescriptorWriter::Open(const string &path, io::Append append) {
	if (fd_ >= 0) {
		close(fd_);
	}

	int flags = O_WRONLY | O_CREAT;
	switch (append) {
	case Append::Disabled:
		flags |= O_TRUNC;
		break;
	case Append::Enabled:
		flags |= O_APPEND;
		break;
	}
	fd_ = open(path.c_str(), flags, 0644);
	if (fd_ < 0) {
		int err = errno;
		return error::Error(generic_category().default_error_condition(err), "Cannot open " + path);
	}
	return error::NoError;
}

ExpectedSize FileDescriptorWriter::Write(
	vector<uint8_t>::const_iterator start, vector<uint8_t>::const_iterator end) {
	auto n = write(fd_, &start[0], end - start);
	if (n < 0) {
		int err = errno;
		return expected::unexpected(error::Error(generic_category().default_error_condition(err), "write() failed"));
	}
	return n;
}

} // namespace io
} // namespace common
} // namespace mender
