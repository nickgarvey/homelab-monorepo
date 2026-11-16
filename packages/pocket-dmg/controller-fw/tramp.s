.syntax unified
.thumb
@ base 0x08015c34; trampoline placed at 0x08016e68
cave:
.org 0x08016e68 - 0x08015c34
bl cave
