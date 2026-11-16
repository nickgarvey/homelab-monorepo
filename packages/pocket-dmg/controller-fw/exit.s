.syntax unified
.thumb
@ base 0x08015b36
movs r0, #0
strb r0, [r6, #4]
strb.w r0, [r1, #0x8f]
b.n tgt
.org 0x08015bc0 - 0x08015b36
tgt:
