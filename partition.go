/**
 * Filename: /Users/htang/code/allhic/allhic/partition.go
 * Path: /Users/htang/code/allhic/allhic
 * Created Date: Wednesday, January 3rd 2018, 11:21:45 am
 * Author: htang
 *
 * Copyright (c) 2018 Haibao Tang
 */

package allhic

import (
	"fmt"
	"math"
	"strconv"
)

// Partitioner converts the bamfile into a matrix of link counts
type Partitioner struct {
	Contigsfile string
	Distfile    string
	K           int
	contigs     []*ContigInfo
	contigToIdx map[string]int
	matrix      [][]int64
	longestRE   int
	clusters    Clusters
	// Output files
	OutREfiles []string
}

// Run is the main function body of partition
func (r *Partitioner) Run() {
	r.readRE()
	r.skipContigsWithFewREs()
	// if r.K == 1 {
	// 	r.makeTrivialClusters()
	// } else {
	r.makeMatrix()
	r.skipRepeats()
	r.Cluster()
	// }
	r.printClusters()
	r.splitRE()
	log.Notice("Success")
}

// makeTrivialClusters make a single cluster containing all contigs
// except the really short ones
func (r *Partitioner) makeTrivialClusters() {
	contigs := []int{}
	for i, contig := range r.contigs {
		if contig.skip {
			continue
		}
		contigs = append(contigs, i)
	}
	clusters := Clusters{
		0: contigs,
	}
	r.clusters = clusters
}

// skipContigsWithFewREs skip contigs with fewere than MinREs
// This reads in the `counts_RE.txt` file generated by extract()
func (r *Partitioner) skipContigsWithFewREs() {
	log.Noticef("skipContigsWithFewREs with MinREs = %d", MinREs)
	nShort := 0
	shortRE := 0
	shortLen := 0

	for i, contig := range r.contigs {
		if contig.recounts < MinREs {
			fmt.Printf("Contig #%d (%s) has %d RE sites -> MARKED SHORT\n",
				i, contig.name, contig.recounts)
			nShort++
			shortRE += contig.recounts
			shortLen += contig.length
			contig.skip = true
		}
	}
	avgRE, avgLen := 0.0, 0
	if nShort > 0 {
		avgRE, avgLen = float64(shortRE)/float64(nShort), shortLen/nShort
	}
	log.Noticef("Marked %d contigs (avg %.1f RE sites, len %d) since they contain too few REs (MinREs = %d)",
		nShort, avgRE, avgLen, MinREs)
}

// skipRepeats skip contigs likely from repetitive regions. Contigs are repetitive if they have more links
// compared to the average contig. This should be run after contig length normalization.
func (r *Partitioner) skipRepeats() {
	log.Noticef("skipRepeats with multiplicity = %d", MaxLinkDensity)
	// Find the number of Hi-C links on each contig
	totalLinks := int64(0)
	N := len(r.contigs)
	nLinks := make([]int64, N)
	for i := 0; i < N; i++ {
		for j := i + 1; j < N; j++ {
			counts := r.matrix[i][j]
			totalLinks += counts
			nLinks[i] += counts
			nLinks[j] += counts
		}
	}

	// Determine the threshold of whether a contig is 'repetitive'
	nLinksAvg := 2.0 * float64(totalLinks) / float64(N)
	nRepetitive := 0
	repetitiveLength := 0
	for i, contig := range r.contigs {
		factor := float64(nLinks[i]) / nLinksAvg
		// Adjust all ink densitities by their repetitive factors
		for j := 0; j < N; j++ {
			if r.matrix[i][j] != 0 {
				r.matrix[i][j] = int64(math.Ceil(float64(r.matrix[i][j]) / factor))
			}
		}

		if factor >= MaxLinkDensity {
			fmt.Printf("Contig #%d (%s) has %.1fx the average number of Hi-C links -> MARKED REPETITIVE\n",
				i, contig.name, factor)
			nRepetitive++
			repetitiveLength += contig.length
			contig.skip = true
		}
	}

	avgRepetiveLength := 0
	if nRepetitive > 0 {
		avgRepetiveLength = repetitiveLength / nRepetitive
	}

	// Note that the contigs reported as repetitive may have already been marked as skip (e.g. skipContigsWithFewREs)
	log.Noticef("Marked %d contigs (avg len %d) as repetitive (MaxLinkDensity = %d)",
		nRepetitive, avgRepetiveLength, MaxLinkDensity)
}

// makeMatrix creates an adjacency matrix containing normalized score
func (r *Partitioner) makeMatrix() {
	edges := r.parseDist()
	N := len(r.contigs)
	M := Make2DSliceInt64(N, N)
	longestSquared := int64(r.longestRE) * int64(r.longestRE)

	// Load up all the contig pairs
	for _, e := range edges {
		a, _ := r.contigToIdx[e.at]
		b, _ := r.contigToIdx[e.bt]
		if a == b {
			continue
		}

		// Just normalize the counts
		w := int64(e.nObservedLinks) * longestSquared / (int64(e.RE1) * int64(e.RE2))
		M[a][b] = w
		M[b][a] = w
	}
	r.matrix = M
}

// readRE reads in a three-column tab-separated file
// #Contig    REcounts    Length
func (r *Partitioner) readRE() {
	recs := ReadCSVLines(r.Contigsfile)
	r.longestRE = 0
	for _, rec := range recs {
		name := rec[0]
		recounts, _ := strconv.Atoi(rec[1])
		length, _ := strconv.Atoi(rec[2])
		ci := &ContigInfo{
			name:     name,
			recounts: recounts,
			length:   length,
		}
		if recounts > r.longestRE {
			r.longestRE = recounts
		}
		r.contigs = append(r.contigs, ci)
	}
	r.contigToIdx = map[string]int{}
	for i, contig := range r.contigs {
		r.contigToIdx[contig.name] = i
	}
	log.Noticef("Loaded %d contig RE lengths for normalization from `%s`",
		len(r.contigs), r.Contigsfile)
}

// splitRE reads in a three-column tab-separated file
// #Contig    REcounts    Length
func (r *Partitioner) splitRE() {
	for j, cl := range r.clusters {
		contigs := []*ContigInfo{}
		for _, idx := range cl {
			contigs = append(contigs, r.contigs[idx])
		}
		outfile := fmt.Sprintf("%s.%dg%d.txt", RemoveExt(r.Contigsfile), r.K, j+1)
		writeRE(outfile, contigs)
		r.OutREfiles = append(r.OutREfiles, outfile)
	}
}

// parseDist imports the edges of the contig into a slice of DistLine
// DistLine stores the data structure of the distfile
// #X      Y       Contig1 Contig2 RE1     RE2     ObservedLinks   ExpectedLinksIfAdjacent
// 1       44      idcChr1.ctg24   idcChr1.ctg51   6612    1793    12      121.7
// 1       70      idcChr1.ctg24   idcChr1.ctg52   6612    686     2       59.3
func (r *Partitioner) parseDist() []ContigPair {
	var edges []ContigPair
	recs := ReadCSVLines(r.Distfile)

	for _, rec := range recs {
		ai, _ := strconv.Atoi(rec[0])
		bi, _ := strconv.Atoi(rec[1])
		at, bt := rec[2], rec[3]
		RE1, _ := strconv.Atoi(rec[4])
		RE2, _ := strconv.Atoi(rec[5])
		nObservedLinks, _ := strconv.Atoi(rec[6])
		nExpectedLinks, _ := strconv.ParseFloat(rec[7], 64)

		cp := ContigPair{
			ai: ai, bi: bi,
			at: at, bt: bt,
			RE1: RE1, RE2: RE2,
			nObservedLinks: nObservedLinks, nExpectedLinks: nExpectedLinks,
		}

		edges = append(edges, cp)
	}

	return edges
}
