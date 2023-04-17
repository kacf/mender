#include <ext/stdio_filebuf.h>
#include <assert.h>
#include <vector>
#include <iterator>
#include <algorithm>
#include <iostream>
#include <version>
#include <list>
#include <fstream>

using namespace std;

template<typename T>
class AnyIterator : public std::iterator<T, T, const T *, T> {
private:
	class AbstractIterator {
	public:
		virtual ~AbstractIterator() {};
		virtual AbstractIterator &operator++() = 0;
		virtual AbstractIterator &operator--() = 0;
		virtual T &operator*() const = 0;
		virtual AbstractIterator &operator+=(ssize_t offset) = 0;
		virtual AbstractIterator &operator-=(ssize_t offset) = 0;

		virtual void CloneTo(char *dst) const = 0;
	};

	template <typename I>
	class Iterator : public AbstractIterator {
	public:
		Iterator(I &i) :
			iterator_(i) {
		}
		~Iterator() {}

		AbstractIterator &operator++() {
			++iterator_;
			return *this;
		}

		AbstractIterator &operator--() {
			--iterator_;
			return *this;
		}

		typename I::value_type &operator*() const {
			return *iterator_;
		}

		Iterator &operator+=(ssize_t offset) {
			iterator_ += offset;
			return *this;
		}
		Iterator &operator-=(ssize_t offset) {
			iterator_ -= offset;
			return *this;
		}

	private:
		void CloneTo(char *dst) const {
			auto p = reinterpret_cast<Iterator<I> *>(dst);
			new (dst) Iterator<I>(*this);
		}

		I iterator_;
	};

public:
	AnyIterator() {}

	template <typename I>
	AnyIterator(I other) {
		static_assert(sizeof(storage_) >= sizeof(Iterator<I>));
		set_ = true;
		AbstractIterator *p = &Self();
		new (p) Iterator<I>(other);
	}

	AnyIterator(const AnyIterator<T> &other) {
		if (set_) {
			Self().~AbstractIterator();
		}
		set_ = other.set_;
		if (set_) {
			other.Self().CloneTo(storage_);
		}
	}

	~AnyIterator() {
		if (set_) {
			Self().~AbstractIterator();
		}
	}

	AnyIterator<T> &operator++() {
		++Self();
		return *this;
	}
	AnyIterator<T> operator++(int) {
		AnyIterator<T> ret(*this);
		++Self();
		return ret;
	}
	AnyIterator<T> &operator--() {
		--Self();
		return *this;
	}
	AnyIterator<T> operator--(int) {
		AnyIterator<T> ret(*this);
		--Self();
		return ret;
	}

	AnyIterator<T> &operator+=(ssize_t offset) {
		Self() += offset;
		return *this;
	}
	AnyIterator<T> &operator-=(ssize_t offset) {
		Self() -= offset;
		return *this;
	}

	AnyIterator<T> operator+(ssize_t offset) const {
		AnyIterator<T> ret(*this);
		ret += offset;
		return ret;
	}
	AnyIterator<T> operator-(ssize_t offset) const {
		AnyIterator<T> ret(*this);
		ret -= offset;
		return ret;
	}

	T &operator*() const {
		return *Self();
	}

	// These can't be done virtually, because we don't know the inner type of `other`. Do these
	// based on memory location.
	bool operator==(const AnyIterator<T> &other) const {
		return &*Self() == &*other.Self();
	}
	bool operator!=(const AnyIterator<T> &other) const {
		return &*Self() != &*other.Self();
	}
	bool operator<(const AnyIterator<T> &other) const {
		return &*Self() < &*other.Self();
	}
	bool operator>(const AnyIterator<T> &other) const {
		return &*Self() > &*other.Self();
	}
	bool operator<=(const AnyIterator<T> &other) const {
		return &*Self() <= &*other.Self();
	}
	bool operator>=(const AnyIterator<T> &other) const {
		return &*Self() >= &*other.Self();
	}
	ptrdiff_t operator-(const AnyIterator<T> &other) const {
		return &*Self() - &*other.Self();
	}

private:
	AbstractIterator &Self() {
		assert(set_);
		return *reinterpret_cast<AbstractIterator *>(storage_);
	}
	const AbstractIterator &Self() const {
		assert(set_);
		return *reinterpret_cast<const AbstractIterator *>(storage_);
	}

	bool set_ {false};
	char storage_[sizeof(char *) * 2];
};

int main() {
	vector<uint8_t> input {1, 2, 3, 4};
	vector<uint8_t> output;
	output.resize(input.size());
	AnyIterator<uint8_t> i1 = input.begin();
	AnyIterator<uint8_t> i2 = input.end();
	AnyIterator<uint8_t> o = output.begin();

	copy(i1, i2, o);
	cout << to_string(output[1]) << endl;

	__gnu_cxx::stdio_filebuf<char> filebuf(0, std::ios::in);
	istream is(&filebuf);
}
