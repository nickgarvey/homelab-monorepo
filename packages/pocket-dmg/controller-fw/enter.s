.syntax unified
.thumb
@ base 0x08015bc0
tgt:
.org 0x08015c1c - 0x08015bc0
movs r3, #0x53
strb.w r3, [r0, #0x8f]      @ R3 / touchpad click -> mouse left, like stock
movw r1, #0x0694
movt r1, #0x2000
ldr r2, [r1]                @ 0x20000694 touch centre X
str r2, [r1, #0xc]          @ -> 0x200006a0 accumulator X (firmware idiom)
ldr r2, [r1, #4]            @ 0x20000698 centre Y
str r2, [r1, #0x10]         @ -> 0x200006a4 accumulator Y
b.n tgt
