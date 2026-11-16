.syntax unified
.thumb
@ base 0x08015c34 (dead code after patched toggle enter path)
cave:
ldrb.w r12, [r7, #0x48]     @ current mouse report button byte (0x20001666)
and r12, r12, #1            @ left button held via R3 (touchpad click)
orr r3, r3, r12             @ keep it pressed in the touch report
strb.w r3, [r7, #0x48]      @ original store
bx lr
