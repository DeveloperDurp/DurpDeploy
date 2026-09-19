export function verifyExactEvents(transcript, expected) {
	const lines = transcript.split("\n").map((line) => line.trim()).filter(Boolean);
	const forbidden = lines.find((line) => /^(FAIL|SKIP)\s/.test(line));
	if (forbidden) throw new Error(`forbidden event: ${forbidden}`);
	const events = lines
		.filter((line) => line.startsWith("PASS "))
		.map((line) => line.slice("PASS ".length));
	if (events.length !== new Set(events).size ||
		events.length !== expected.length ||
		events.some((event, index) => event !== expected[index])) {
		throw new Error(
			`strict event mismatch: got=${JSON.stringify(events)} ` +
			`want=${JSON.stringify(expected)}`,
		);
	}
	return events;
}
