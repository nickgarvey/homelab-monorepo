.syntax unified
.thumb
@ base 0x08016bc8
cmp r2, #2
revsh r1, r1
strh r0, [r6, #4]
bcc.n x2path
beq.n ret
ldrh r2, [r7, #0x16]
ldrd r3, r5, [r6, #0x34]
ldrh r7, [r7, #0x18]
smlabb r1, r2, r1, r3
smlabb r0, r7, r0, r5
strd r1, r0, [r6, #0x34]
nop
ret:
.org 0x08016bf0 - 0x08016bc8
x2path:
