'use strict'

const values = [1, 2, 3].map((value) => value * 2)
if (values.join(',') !== '2,4,6') {
  throw new Error('array/map smoke test failed')
}

const bytes = new Uint8Array([0x4d, 0x4c, 0x48])
if (bytes.length !== 3 || bytes[0] !== 0x4d) {
  throw new Error('typed array smoke test failed')
}

const result = (() => {
  const value = 40
  return value + 2
})()
if (result !== 42) {
  throw new Error('function/closure smoke test failed')
}

print('QuickJS-NG JavaScript smoke test passed')
