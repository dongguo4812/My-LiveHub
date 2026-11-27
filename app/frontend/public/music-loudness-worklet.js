const TARGET_LUFS = -16
const MAX_BOOST_DB = 12
const MAX_CUT_DB = -18
const TRUE_PEAK_CEILING = Math.pow(10, -1 / 20)

function clamp(value, minimum, maximum) {
  return Math.min(maximum, Math.max(minimum, value))
}

function highPassCoefficients(frequency, q) {
  const omega = 2 * Math.PI * frequency / sampleRate
  const cosine = Math.cos(omega)
  const alpha = Math.sin(omega) / (2 * q)
  const a0 = 1 + alpha
  return {
    b0: (1 + cosine) / (2 * a0),
    b1: -(1 + cosine) / a0,
    b2: (1 + cosine) / (2 * a0),
    a1: (-2 * cosine) / a0,
    a2: (1 - alpha) / a0,
  }
}

function highShelfCoefficients(frequency, gainDB, q) {
  const amplitude = Math.pow(10, gainDB / 40)
  const omega = 2 * Math.PI * frequency / sampleRate
  const cosine = Math.cos(omega)
  const alpha = Math.sin(omega) / (2 * q)
  const root = Math.sqrt(amplitude)
  const a0 = (amplitude + 1) - (amplitude - 1) * cosine + 2 * root * alpha
  return {
    b0: amplitude * ((amplitude + 1) + (amplitude - 1) * cosine + 2 * root * alpha) / a0,
    b1: -2 * amplitude * ((amplitude - 1) + (amplitude + 1) * cosine) / a0,
    b2: amplitude * ((amplitude + 1) + (amplitude - 1) * cosine - 2 * root * alpha) / a0,
    a1: 2 * ((amplitude - 1) - (amplitude + 1) * cosine) / a0,
    a2: ((amplitude + 1) - (amplitude - 1) * cosine - 2 * root * alpha) / a0,
  }
}

class Biquad {
  constructor(coefficients) {
    Object.assign(this, coefficients)
    this.z1 = 0
    this.z2 = 0
  }

  process(input) {
    const output = this.b0 * input + this.z1
    this.z1 = this.b1 * input - this.a1 * output + this.z2
    this.z2 = this.b2 * input - this.a2 * output
    return output
  }
}

class LiveLoudnessNormalizer extends AudioWorkletProcessor {
  constructor(options) {
    super()
    this.targetLUFS = Number.isFinite(options.processorOptions?.targetLUFS)
      ? options.processorOptions.targetLUFS
      : TARGET_LUFS
    this.lookaheadFrames = Math.max(1, Math.round(sampleRate * 0.005))
    this.bufferLength = this.lookaheadFrames + 1
    this.sampleBuffers = []
    this.filters = []
    this.history = []
    this.writeIndex = 0
    this.frameCounter = 0

    this.energyRing = new Float64Array(Math.ceil(sampleRate * 3 / 128) + 2)
    this.energyCountRing = new Float64Array(this.energyRing.length)
    this.energyIndex = 0
    this.energyBlocks = 0
    this.energySum = 0
    this.energySampleCount = 0
    this.gain = 1
    this.limiterGain = 1

    this.dequeLength = this.lookaheadFrames + 2
    this.peakValues = new Float32Array(this.dequeLength)
    this.peakIndices = new Float64Array(this.dequeLength)
    this.dequeHead = 0
    this.dequeTail = 0
    this.port.onmessage = event => {
      if (event.data?.type === 'reset') this.reset()
    }
  }

  reset() {
    for (const buffer of this.sampleBuffers) buffer.fill(0)
    for (const history of this.history) history.fill(0)
    this.writeIndex = 0
    this.frameCounter = 0
    this.energyRing.fill(0)
    this.energyCountRing.fill(0)
    this.energyIndex = 0
    this.energyBlocks = 0
    this.energySum = 0
    this.energySampleCount = 0
    this.gain = 1
    this.limiterGain = 1
    this.peakValues.fill(0)
    this.peakIndices.fill(0)
    this.dequeHead = 0
    this.dequeTail = 0
    this.filters = this.filters.map(() => ({
      highPass: new Biquad(highPassCoefficients(38.135470876, 0.5003270373238773)),
      highShelf: new Biquad(highShelfCoefficients(1681.974450955533, 3.999843853973347, 0.7071752369554196)),
    }))
  }

  ensureChannels(count) {
    while (this.sampleBuffers.length < count) {
      this.sampleBuffers.push(new Float32Array(this.bufferLength))
      this.history.push([0, 0, 0])
      this.filters.push({
        highPass: new Biquad(highPassCoefficients(38.135470876, 0.5003270373238773)),
        highShelf: new Biquad(highShelfCoefficients(1681.974450955533, 3.999843853973347, 0.7071752369554196)),
      })
    }
  }

  estimateTruePeak(channel, sample) {
    const history = this.history[channel]
    const p0 = history[0]
    const p1 = history[1]
    const p2 = history[2]
    const p3 = sample
    const a = p1
    const b = 0.5 * (-p0 + p2)
    const c = 0.5 * (2 * p0 - 5 * p1 + 4 * p2 - p3)
    const d = 0.5 * (-p0 + 3 * p1 - 3 * p2 + p3)
    let peak = Math.abs(sample)
    let value = ((d * 0.25 + c) * 0.25 + b) * 0.25 + a
    peak = Math.max(peak, Math.abs(value))
    value = ((d * 0.5 + c) * 0.5 + b) * 0.5 + a
    peak = Math.max(peak, Math.abs(value))
    value = ((d * 0.75 + c) * 0.75 + b) * 0.75 + a
    peak = Math.max(peak, Math.abs(value))
    history[0] = p1
    history[1] = p2
    history[2] = p3
    return peak
  }

  pushPeak(framePeak) {
    const frame = this.frameCounter
    while (this.dequeTail > this.dequeHead &&
      this.peakValues[(this.dequeTail - 1) % this.dequeLength] <= framePeak) {
      this.dequeTail--
    }
    const tail = this.dequeTail % this.dequeLength
    this.peakValues[tail] = framePeak
    this.peakIndices[tail] = frame
    this.dequeTail++
    const oldest = frame - this.lookaheadFrames
    while (this.dequeHead < this.dequeTail &&
      this.peakIndices[this.dequeHead % this.dequeLength] < oldest) {
      this.dequeHead++
    }
    return this.dequeHead < this.dequeTail
      ? this.peakValues[this.dequeHead % this.dequeLength]
      : framePeak
  }

  updateLoudness(blockEnergy, sampleCount, frameCount) {
    const index = this.energyIndex
    if (this.energyBlocks === this.energyRing.length) {
      this.energySum -= this.energyRing[index]
      this.energySampleCount -= this.energyCountRing[index]
    } else {
      this.energyBlocks++
    }
    this.energyRing[index] = blockEnergy
    this.energyCountRing[index] = sampleCount
    this.energySum += blockEnergy
    this.energySampleCount += sampleCount
    this.energyIndex = (index + 1) % this.energyRing.length

    // Wait for a meaningful momentary window before applying gain. The ring
    // then naturally grows into the 3-second short-term measurement window.
    if (this.energySampleCount < sampleRate * 0.4) return
    const meanSquare = this.energySum / Math.max(1, this.energySampleCount)
    if (!Number.isFinite(meanSquare) || meanSquare < 1e-9) return
    const lufs = -0.691 + 10 * Math.log10(meanSquare)
    const gainDB = clamp(this.targetLUFS - lufs, MAX_CUT_DB, MAX_BOOST_DB)
    const targetGain = Math.pow(10, gainDB / 20)
    const timeConstant = targetGain < this.gain ? 0.25 : 1.5
    const smoothing = 1 - Math.exp(-frameCount / (sampleRate * timeConstant))
    this.gain += (targetGain - this.gain) * smoothing
  }

  process(inputs, outputs) {
    const output = outputs[0]
    if (!output || output.length === 0) return true
    const input = inputs[0]
    if (!input || input.length === 0) {
      for (const channel of output) channel.fill(0)
      return true
    }

    const channels = Math.min(input.length, output.length)
    this.ensureChannels(channels)
    const frames = output[0].length
    let blockEnergy = 0

    for (let frame = 0; frame < frames; frame++) {
      let framePeak = 0
      for (let channel = 0; channel < channels; channel++) {
        const sample = input[channel][frame] || 0
        const filter = this.filters[channel]
        const weighted = filter.highShelf.process(filter.highPass.process(sample))
        blockEnergy += weighted * weighted
        framePeak = Math.max(framePeak, this.estimateTruePeak(channel, sample))
        this.sampleBuffers[channel][this.writeIndex] = sample
      }

      const windowPeak = this.pushPeak(framePeak)
      const limiterTarget = windowPeak * this.gain > TRUE_PEAK_CEILING
        ? TRUE_PEAK_CEILING / (windowPeak * this.gain)
        : 1
      if (limiterTarget < this.limiterGain) {
        this.limiterGain = limiterTarget
      } else {
        const release = 1 - Math.exp(-1 / (sampleRate * 0.05))
        this.limiterGain += (1 - this.limiterGain) * release
      }

      const delayedFrame = this.frameCounter - this.lookaheadFrames
      if (delayedFrame >= 0) {
        const readIndex = delayedFrame % this.bufferLength
        const appliedGain = this.gain * this.limiterGain
        for (let channel = 0; channel < channels; channel++) {
          output[channel][frame] = this.sampleBuffers[channel][readIndex] * appliedGain
        }
      } else {
        for (let channel = 0; channel < channels; channel++) output[channel][frame] = 0
      }

      this.frameCounter++
      this.writeIndex = (this.writeIndex + 1) % this.bufferLength
    }

    this.updateLoudness(blockEnergy, frames, frames)
    return true
  }
}

registerProcessor('mylivehub-loudness-normalizer', LiveLoudnessNormalizer)
