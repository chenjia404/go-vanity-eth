// secp256k1 素域、批量求逆和以太坊地址计算；私钥不传入显卡。
#include <stdint.h>

typedef unsigned int U;
typedef unsigned long long W;
struct F { U v[8]; };
struct Point { F x, y; };
struct Rules { U continuous, count, lengths[32]; unsigned char patterns[32][40]; };
struct Hit { U lane, iteration; unsigned char address[20]; };
// 固定共享数据的布局，防止 Go 与内核之间的结构尺寸发生偏移。
static_assert(sizeof(F)==32 && sizeof(Point)==64, "");
static_assert(sizeof(Rules)==1416 && sizeof(Hit)==28, "");

__device__ __forceinline__ F one() { F a = {}; a.v[0] = 1; return a; }
__device__ __forceinline__ bool zero(F a) {
    U x = 0;
    #pragma unroll
    for (int i=0; i<8; ++i) x |= a.v[i];
    return x == 0;
}
__device__ __forceinline__ bool equal(F a, F b) {
    U x = 0;
    #pragma unroll
    for (int i=0; i<8; ++i) x |= a.v[i] ^ b.v[i];
    return x == 0;
}
__device__ __forceinline__ F normalize(F a) {
    bool ge = true;
    for (int i=7; i>=0; --i) {
        U p = i==0 ? 0xfffffc2fU : (i==1 ? 0xfffffffeU : 0xffffffffU);
        if (a.v[i] != p) { ge = a.v[i] > p; break; }
    }
    if (ge) {
        W borrow = 0;
        #pragma unroll
        for (int i=0; i<8; ++i) {
            W p = (i==0 ? 0xfffffc2fU : (i==1 ? 0xfffffffeU : 0xffffffffU));
            W sub = p + borrow;
            borrow = (W)a.v[i] < sub;
            a.v[i] = (U)((W)a.v[i] - sub);
        }
    }
    return a;
}
__device__ __forceinline__ void addword(U *t, int pos, W value) {
    while (value) { value += t[pos]; t[pos++] = (U)value; value >>= 32; }
}
__device__ __forceinline__ F mul(F a, F b) {
    U t[18] = {};
    #pragma unroll
    for (int i=0; i<8; ++i) {
        W carry = 0;
        #pragma unroll
        for (int j=0; j<8; ++j) {
            W q = (W)a.v[i]*b.v[j] + t[i+j] + carry;
            t[i+j] = (U)q; carry = q >> 32;
        }
        t[i+8] = (U)carry;
    }
    // 2^256 与 2^32+977 在本素域内同余。
    for (int i=15; i>=8; --i) {
        U h = t[i]; t[i] = 0;
        addword(t, i-8, (W)h*977);
        addword(t, i-7, h);
    }
    while (t[8]) {
        U h = t[8]; t[8] = 0;
        addword(t, 0, (W)h*977); addword(t, 1, h);
    }
    F r;
    #pragma unroll
    for (int i=0; i<8; ++i) r.v[i] = t[i];
    return normalize(r);
}
__device__ __forceinline__ F add(F a, F b) {
    F r; W carry = 0;
    #pragma unroll
    for (int i=0; i<8; ++i) { W q=(W)a.v[i]+b.v[i]+carry; r.v[i]=(U)q; carry=q>>32; }
    while (carry) {
        W q=(W)r.v[0]+977; r.v[0]=(U)q; carry=q>>32;
        q=(W)r.v[1]+1+carry; r.v[1]=(U)q; carry=q>>32;
        for (int i=2; i<8; ++i) { q=(W)r.v[i]+carry; r.v[i]=(U)q; carry=q>>32; }
    }
    return normalize(r);
}
__device__ __forceinline__ F sub(F a, F b) {
    F r; W borrow=0;
    #pragma unroll
    for (int i=0; i<8; ++i) { W q=(W)b.v[i]+borrow; borrow=(W)a.v[i]<q; r.v[i]=(U)((W)a.v[i]-q); }
    if (borrow) {
        W q=977; W br=0;
        #pragma unroll
        for (int i=0; i<8; ++i) {
            W s=(i==0 ? q : (i==1 ? 1ULL : 0ULL))+br;
            br=(W)r.v[i]<s; r.v[i]=(U)((W)r.v[i]-s);
        }
    }
    return r;
}
__device__ F inverse(F a) {
    F r=one();
    for (int i=255; i>=0; --i) {
        r=mul(r,r);
        U limb = i/32==0 ? 0xfffffc2dU : (i/32==1 ? 0xfffffffeU : 0xffffffffU);
        if ((limb>>(i%32))&1) r=mul(r,a);
    }
    return r;
}
__device__ __forceinline__ F shuffle(F a, int lane) {
    F r;
    #pragma unroll
    for (int i=0; i<8; ++i) r.v[i]=__shfl_sync(0xffffffffU,a.v[i],lane);
    return r;
}
__device__ F warpInverse(F d) {
    int lane=threadIdx.x&31;
    F prefix=d, suffix=d;
    for (int offset=1; offset<32; offset*=2) {
        F p=shuffle(prefix, lane>=offset ? lane-offset : 0);
        F s=shuffle(suffix, lane+offset<32 ? lane+offset : 31);
        if (lane>=offset) prefix=mul(p,prefix);
        if (lane+offset<32) suffix=mul(s,suffix);
    }
    F total=shuffle(prefix,31), inv=one();
    if (lane==31) inv=inverse(total);
    inv=shuffle(inv,31);
    F p=shuffle(prefix,lane ? lane-1 : 0);
    F s=shuffle(suffix,lane<31 ? lane+1 : 31);
    if (lane) inv=mul(inv,p);
    if (lane<31) inv=mul(inv,s);
    return inv;
}

__device__ __constant__ W rc[24]={
    0x1ULL,0x8082ULL,0x800000000000808aULL,0x8000000080008000ULL,
    0x808bULL,0x80000001ULL,0x8000000080008081ULL,0x8000000000008009ULL,
    0x8aULL,0x88ULL,0x80008009ULL,0x8000000aULL,0x8000808bULL,
    0x800000000000008bULL,0x8000000000008089ULL,0x8000000000008003ULL,
    0x8000000000008002ULL,0x8000000000000080ULL,0x800aULL,
    0x800000008000000aULL,0x8000000080008081ULL,0x8000000000008080ULL,
    0x80000001ULL,0x8000000080008008ULL};
__device__ __forceinline__ W rot(W x,int n) { return n ? (x<<n)|(x>>(64-n)) : x; }
__device__ void keccak(const unsigned char *in,int len,unsigned char *out) {
    W a[25]={}, b[25], c[5], d[5];
    const int offsets[25]={0,1,62,28,27,36,44,6,55,20,3,10,43,25,39,41,45,15,21,8,18,2,61,56,14};
    for (int i=0;i<len;++i) a[i/8]^=(W)in[i]<<(8*(i%8));
    a[len/8]^=1ULL<<(8*(len%8)); a[16]^=0x8000000000000000ULL;
    for (int round=0;round<24;++round) {
        #pragma unroll
        for(int x=0;x<5;++x) c[x]=a[x]^a[x+5]^a[x+10]^a[x+15]^a[x+20];
        #pragma unroll
        for(int x=0;x<5;++x) d[x]=c[(x+4)%5]^rot(c[(x+1)%5],1);
        #pragma unroll
        for(int y=0;y<5;++y) {
            #pragma unroll
            for(int x=0;x<5;++x) b[y+5*((2*x+3*y)%5)]=rot(a[x+5*y]^d[x],offsets[x+5*y]);
        }
        #pragma unroll
        for(int y=0;y<5;++y) {
            #pragma unroll
            for(int x=0;x<5;++x) a[x+5*y]=b[x+5*y]^((~b[(x+1)%5+5*y])&b[(x+2)%5+5*y]);
        }
        a[0]^=rc[round];
    }
    for(int i=0;i<32;++i) out[i]=(unsigned char)(a[i/8]>>(8*(i%8)));
}
__device__ void address(Point p,unsigned char *out) {
    unsigned char pub[64], hash[32];
    for(int i=0;i<32;++i) {
        pub[i]=(unsigned char)(p.x.v[7-i/4]>>(24-8*(i%4)));
        pub[i+32]=(unsigned char)(p.y.v[7-i/4]>>(24-8*(i%4)));
    }
    keccak(pub,64,hash);
    for(int i=0;i<20;++i) out[i]=hash[i+12];
}
__device__ bool matches(const unsigned char *addr,const Rules *rules) {
    unsigned char text[40],hash[32];
    for(int i=0;i<40;++i) {
        int n=(addr[i/2]>>(i%2 ? 0 : 4))&15;
        text[i]=(unsigned char)(n<10 ? '0'+n : 'a'+n-10);
    }
    // 与 go-ethereum 的 EIP-55 大小写匹配规则保持一致。
    keccak(text,40,hash);
    for(int i=0;i<40;++i) if(text[i]>='a' && ((hash[i/2]>>(i%2 ? 0 : 4))&15)>=8) text[i]-=32;
    int n=rules->continuous;
    if(n) {
        bool first=true,last=true;
        for(int i=1;i<n;++i) { first &= text[i]==text[0]; last &= text[40-n+i]==text[40-n]; }
        if(first || last) return true;
    }
    for(int p=0;p<rules->count;++p) {
        int len=rules->lengths[p];
        for(int i=0;i<=40-len;++i) {
            bool ok=true;
            for(int j=0;j<len;++j) if(text[i+j]!=rules->patterns[p][j]) { ok=false; break; }
            if(ok) return true;
        }
    }
    return false;
}
extern "C" __global__ void search(Point *points,const Rules *rules,Hit *hits,U *count,U *error,U steps,U all) {
    U lane=blockIdx.x*blockDim.x+threadIdx.x;
    Point p=points[lane];
    const F gx={{0x16f81798U,0x59f2815bU,0x2dce28d9U,0x029bfcdbU,0xce870b07U,0x55a06295U,0xf9dcbbacU,0x79be667eU}};
    const F gy={{0xfb10d4b8U,0x9c47d08fU,0xa6855419U,0xfd17b448U,0x0e1108a8U,0x5da4fbfcU,0x26a3c465U,0x483ada77U}};
    for(U iteration=0;iteration<steps;++iteration) {
        unsigned char addr[20]; address(p,addr);
        if(all || matches(addr,rules)) {
            U i=atomicAdd(count,1U);
            hits[i].lane=lane; hits[i].iteration=iteration;
            for(int j=0;j<20;++j) hits[i].address[j]=addr[j];
        }
        F numerator=sub(gy,p.y), denominator=sub(gx,p.x);
        if(zero(denominator)) {
            if(equal(p.y,gy)) {
                F xx=mul(p.x,p.x); numerator=add(add(xx,xx),xx); denominator=add(p.y,p.y);
            } else { atomicExch(error,1U); denominator=one(); }
        }
        F slope=mul(numerator,warpInverse(denominator));
        F x=sub(sub(mul(slope,slope),p.x),gx);
        p.y=sub(mul(slope,sub(p.x,x)),p.y); p.x=x;
    }
    points[lane]=p;
}

// 实机测试入口：将显卡素域运算与 Go 大整数结果逐项比较。
extern "C" __global__ void fieldtest(const F *a,const F *b,F *out,U n) {
    U i=blockIdx.x*blockDim.x+threadIdx.x;
    if(i<n) { out[3*i]=add(a[i],b[i]); out[3*i+1]=sub(a[i],b[i]); out[3*i+2]=mul(a[i],b[i]); }
}
