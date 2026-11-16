class LiveHubMicAnalyser extends AudioWorkletProcessor {
  constructor() {
    super();
    this.blocks = 0;
  }

  process(inputs) {
    const channel = inputs[0]?.[0];
    if (!channel || channel.length === 0) return true;

    let sum = 0;
    for (let i = 0; i < channel.length; i++) {
      sum += channel[i] * channel[i];
    }

    // About 47 updates per second at 48 kHz / 128 samples.
    if (++this.blocks >= 8) {
      this.blocks = 0;
      this.port.postMessage({ rms: Math.sqrt(sum / channel.length) });
    }
    return true;
  }
}

registerProcessor('livehub-mic-analyser', LiveHubMicAnalyser);
