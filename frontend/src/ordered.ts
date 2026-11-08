// One backend call at a time, in the order the events happened: Wails serves concurrent calls on
// separate goroutines, so fast input (keys, key down/up, pointer moves) could otherwise arrive swapped.
export function ordered(): (call: () => Promise<unknown>) => void {
  let chain: Promise<unknown> = Promise.resolve();
  return (call) => {
    chain = chain.then(call).catch(() => { /* a closed session reports itself through its events */ });
  };
}
