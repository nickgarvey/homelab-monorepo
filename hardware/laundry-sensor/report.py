# Prints the netlist from circuit.py as Markdown: parts with LCSC numbers, then every net and its pins.
import builtins
import collections

import circuit  # noqa: F401  (builds the circuit)

c = builtins.default_circuit
print("| Ref | Part | Value | Footprint | LCSC |\n|---|---|---|---|---|")
key = lambda p: (p.ref.rstrip("0123456789"), int(p.ref[len(p.ref.rstrip("0123456789")):]))
for p in sorted(c.parts, key=key):
    fp = (p.footprint or "").split(":")[-1]
    print(f"| {p.ref} | {p.name} | {p.value} | {fp} | {p.fields.get('LCSC', '')} |")
print()
nets = collections.defaultdict(list)
for p in c.parts:
    for pin in p.pins:
        for n in pin.nets:
            if n.name.startswith("__NOCONNECT"):
                continue
            nets[n.name].append(f"{p.ref}.{pin.name or pin.num}")
print("| Net | Pins |\n|---|---|")
for name in sorted(nets):
    print(f"| {name} | {', '.join(sorted(set(nets[name])))} |")
